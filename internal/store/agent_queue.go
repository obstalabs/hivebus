package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
)

const defaultInboxLimit = 32

var (
	ErrAgentSessionNotFound   = errors.New("agent session not found")
	ErrAgentMessageNotFound   = errors.New("agent message not found")
	ErrAgentMessageFinalized  = errors.New("agent message is already finalized")
	ErrAgentMessageWrongQueue = errors.New("agent message does not belong to the requesting session")
)

type AgentSession struct {
	AgentID           string                   `json:"agent_id"`
	InstallationID    string                   `json:"installation_id"`
	SessionID         string                   `json:"session_id"`
	ParticipantID     string                   `json:"participant_id"`
	Capabilities      []string                 `json:"capabilities,omitempty"`
	Roles             []string                 `json:"roles,omitempty"`
	AnswerPublicKey   string                   `json:"answer_public_key,omitempty"` // WO-122: session discovery key; trust stays with askers.
	DeliveryMode      model.AgentDeliveryMode  `json:"delivery_mode"`
	SessionStatus     model.AgentSessionStatus `json:"session_status"`
	LeaseExpiresAt    time.Time                `json:"lease_expires_at"`
	HostAlias         string                   `json:"host_alias,omitempty"`
	ReplacesSessionID string                   `json:"replaces_session_id,omitempty"`
	RegisteredAt      time.Time                `json:"registered_at"`
	LastSeenAt        time.Time                `json:"last_seen_at"`
}

type AgentMessageInput struct {
	MessageID           string `json:"message_id"`
	SenderSessionID     string `json:"sender_session_id"`
	SenderParticipantID string `json:"sender_participant_id"`
	TargetParticipantID string `json:"target_participant_id"`
	ChannelID           string `json:"channel_id,omitempty"`
	Body                string `json:"body"`
	TTL                 time.Duration
}

type AgentMessage struct {
	MessageID             string                     `json:"message_id"`
	SenderSessionID       string                     `json:"sender_session_id"`
	SenderParticipantID   string                     `json:"sender_participant_id"`
	TargetParticipantID   string                     `json:"target_participant_id"`
	TargetAgentID         string                     `json:"target_agent_id,omitempty"`
	TargetAnswerPublicKey string                     `json:"target_answer_public_key,omitempty"` // WO-122: send response exposes the target session's declared key.
	ChannelID             string                     `json:"channel_id,omitempty"`
	Body                  string                     `json:"body"`
	CreatedAt             time.Time                  `json:"created_at"`
	ExpiresAt             time.Time                  `json:"expires_at"`
	State                 model.DeliveryReceiptState `json:"state"`
	DeliveredSessionID    string                     `json:"delivered_session_id,omitempty"`
	DeliveredAt           time.Time                  `json:"delivered_at,omitempty"`
	Reason                string                     `json:"reason,omitempty"`
}

type AgentMessageEvent struct {
	Sequence        int64                      `json:"sequence"`
	MessageID       string                     `json:"message_id"`
	EventAt         time.Time                  `json:"event_at"`
	State           model.DeliveryReceiptState `json:"state"`
	TargetSessionID string                     `json:"target_session_id,omitempty"`
	Reason          string                     `json:"reason,omitempty"`
	ExpiresAt       time.Time                  `json:"expires_at,omitempty"`
	QueuePosition   int                        `json:"queue_position,omitempty"`
}

type AgentMessageRecord struct {
	Message AgentMessage        `json:"message"`
	Channel *model.Channel      `json:"channel,omitempty"`
	Events  []AgentMessageEvent `json:"events"`
}

var ErrChannelNotFound = errors.New("channel not found")

func (s *Store) UpsertChannel(
	ctx context.Context,
	channel model.Channel,
) (model.Channel, error) {
	if s == nil || s.db == nil {
		return model.Channel{}, errors.New("store is not initialized")
	}
	if err := channel.Validate(); err != nil {
		return model.Channel{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_channels (
			channel_id,
			display_name,
			description,
			restricted,
			allowed_participants,
			allowed_agents,
			allowed_roles,
			created_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			display_name = excluded.display_name,
			description = excluded.description,
			restricted = excluded.restricted,
			allowed_participants = excluded.allowed_participants,
			allowed_agents = excluded.allowed_agents,
			allowed_roles = excluded.allowed_roles,
			updated_at = excluded.updated_at
	`,
		channel.ChannelID,
		channel.DisplayName,
		channel.Description,
		boolToInt(channel.Restricted),
		joinCapabilities(channel.AllowedParticipants),
		joinCapabilities(channel.AllowedAgents),
		joinCapabilities(channel.AllowedRoles),
		formatTime(channel.CreatedAt),
		formatTime(channel.UpdatedAt),
	); err != nil {
		return model.Channel{}, fmt.Errorf("upsert channel: %w", err)
	}

	return s.LoadChannel(ctx, channel.ChannelID)
}

func (s *Store) LoadChannel(ctx context.Context, channelID string) (model.Channel, error) {
	if s == nil || s.db == nil {
		return model.Channel{}, errors.New("store is not initialized")
	}
	if strings.TrimSpace(channelID) == "" {
		return model.Channel{}, errors.New("channel_id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	return loadChannelRow(s.db.QueryRowContext(ctx, `
		SELECT
			channel_id,
			display_name,
			description,
			restricted,
			allowed_participants,
			allowed_agents,
			allowed_roles,
			created_at,
			updated_at
		FROM agent_channels
		WHERE channel_id = ?
	`, channelID))
}

func (s *Store) ensureAgentSessionAnswerPublicKeyColumn(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(agent_sessions)`)
	if err != nil {
		return fmt.Errorf("inspect agent_sessions schema: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("scan agent_sessions schema: %w", err)
		}
		if name == "answer_public_key" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate agent_sessions schema: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, `
		ALTER TABLE agent_sessions ADD COLUMN answer_public_key TEXT NOT NULL DEFAULT ''
	`); err != nil {
		return fmt.Errorf("add answer_public_key to agent_sessions: %w", err)
	}

	return nil
}

func (s *Store) RegisterAgentSession(
	ctx context.Context,
	payload model.AgentSessionPayload,
	eventType model.MessageType,
	at time.Time,
) (AgentSession, error) {
	if s == nil || s.db == nil {
		return AgentSession{}, errors.New("store is not initialized")
	}
	if err := payload.Validate(eventType); err != nil {
		return AgentSession{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureAgentSessionAnswerPublicKeyColumn(ctx); err != nil {
		return AgentSession{}, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	leaseExpiresAt, err := time.Parse(time.RFC3339, payload.LeaseExpiresAt)
	if err != nil {
		return AgentSession{}, fmt.Errorf("parse lease_expires_at: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentSession{}, fmt.Errorf("begin agent session transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if eventType == model.MessageTypeAgentSessionRegistered {
		if _, err := tx.ExecContext(ctx, `
			UPDATE agent_sessions
			SET session_status = ?, last_seen_at = ?
			WHERE participant_id = ? AND session_id <> ? AND session_status = ?
		`,
			string(model.AgentSessionReplaced),
			formatTime(at),
			payload.ParticipantID,
			payload.SessionID,
			string(model.AgentSessionOnline),
		); err != nil {
			return AgentSession{}, fmt.Errorf("replace previous agent sessions: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_sessions (
			agent_id,
			installation_id,
			session_id,
			participant_id,
			capabilities_json,
			roles_json,
			answer_public_key,
			delivery_mode,
			session_status,
			lease_expires_at,
			host_alias,
			replaces_session_id,
			registered_at,
			last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			agent_id = excluded.agent_id,
			installation_id = excluded.installation_id,
			participant_id = excluded.participant_id,
			capabilities_json = excluded.capabilities_json,
			roles_json = excluded.roles_json,
			answer_public_key = excluded.answer_public_key,
			delivery_mode = excluded.delivery_mode,
			session_status = excluded.session_status,
			lease_expires_at = excluded.lease_expires_at,
			host_alias = excluded.host_alias,
			replaces_session_id = excluded.replaces_session_id,
			registered_at = CASE
				WHEN agent_sessions.registered_at = '' THEN excluded.registered_at
				ELSE agent_sessions.registered_at
			END,
			last_seen_at = excluded.last_seen_at
	`,
		payload.AgentID,
		payload.InstallationID,
		payload.SessionID,
		payload.ParticipantID,
		joinCapabilities(payload.Capabilities),
		joinCapabilities(payload.Roles),
		strings.TrimSpace(payload.AnswerPublicKey),
		string(payload.DeliveryMode),
		string(payload.SessionStatus),
		formatTime(leaseExpiresAt),
		payload.HostAlias,
		payload.ReplacesSessionID,
		formatTime(at),
		formatTime(at),
	); err != nil {
		return AgentSession{}, fmt.Errorf("upsert agent session: %w", err)
	}

	session, err := loadAgentSessionRow(tx.QueryRowContext(ctx, `
		SELECT
			agent_id,
			installation_id,
			session_id,
			participant_id,
			capabilities_json,
			roles_json,
			answer_public_key,
			delivery_mode,
			session_status,
			lease_expires_at,
			host_alias,
			replaces_session_id,
			registered_at,
			last_seen_at
		FROM agent_sessions
		WHERE session_id = ?
	`, payload.SessionID))
	if err != nil {
		return AgentSession{}, err
	}

	if err := tx.Commit(); err != nil {
		return AgentSession{}, fmt.Errorf("commit agent session transaction: %w", err)
	}

	return session, nil
}

func (s *Store) QueueAgentMessage(
	ctx context.Context,
	input AgentMessageInput,
	now time.Time,
) (AgentMessageRecord, error) {
	if s == nil || s.db == nil {
		return AgentMessageRecord{}, errors.New("store is not initialized")
	}
	if strings.TrimSpace(input.MessageID) == "" {
		return AgentMessageRecord{}, errors.New("message_id is required")
	}
	if strings.TrimSpace(input.SenderSessionID) == "" {
		return AgentMessageRecord{}, errors.New("sender_session_id is required")
	}
	if strings.TrimSpace(input.SenderParticipantID) == "" {
		return AgentMessageRecord{}, errors.New("sender_participant_id is required")
	}
	if strings.TrimSpace(input.TargetParticipantID) == "" {
		return AgentMessageRecord{}, errors.New("target_participant_id is required")
	}
	if strings.TrimSpace(input.Body) == "" {
		return AgentMessageRecord{}, errors.New("body is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureAgentSessionAnswerPublicKeyColumn(ctx); err != nil {
		return AgentMessageRecord{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if input.TTL <= 0 {
		input.TTL = time.Hour
	}
	expiresAt := now.Add(input.TTL)

	if strings.TrimSpace(input.ChannelID) != "" {
		if _, err := s.LoadChannel(ctx, input.ChannelID); err != nil {
			return AgentMessageRecord{}, err
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentMessageRecord{}, fmt.Errorf("begin queue agent message transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := expireQueuedAgentMessagesTx(ctx, tx, now); err != nil {
		return AgentMessageRecord{}, err
	}

	queuePosition, err := queuedMessagePositionTx(ctx, tx, input.TargetParticipantID)
	if err != nil {
		return AgentMessageRecord{}, err
	}
	queuePosition++

	var targetSessionID string
	var targetAgentID string
	var targetAnswerPublicKey string
	row := tx.QueryRowContext(ctx, `
		SELECT session_id, agent_id, answer_public_key
		FROM agent_sessions
		WHERE participant_id = ? AND session_status = ? AND lease_expires_at > ?
		ORDER BY last_seen_at DESC
		LIMIT 1
	`, input.TargetParticipantID, string(model.AgentSessionOnline), formatTime(now))
	switch err := row.Scan(&targetSessionID, &targetAgentID, &targetAnswerPublicKey); {
	case errors.Is(err, sql.ErrNoRows):
		targetSessionID = ""
		targetAgentID = ""
	case err != nil:
		return AgentMessageRecord{}, fmt.Errorf("lookup target agent session: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_messages (
			message_id,
			sender_session_id,
			sender_participant_id,
			target_participant_id,
			target_agent_id,
			target_answer_public_key,
			channel_id,
			body,
			created_at,
			expires_at,
			state,
			delivered_session_id,
			delivered_at,
			reason
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', '')
	`,
		input.MessageID,
		input.SenderSessionID,
		input.SenderParticipantID,
		input.TargetParticipantID,
		targetAgentID,
		targetAnswerPublicKey,
		input.ChannelID,
		input.Body,
		formatTime(now),
		formatTime(expiresAt),
		string(model.DeliveryReceiptQueued),
	); err != nil {
		return AgentMessageRecord{}, fmt.Errorf("insert agent message: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_message_events (
			message_id,
			event_at,
			state,
			target_session_id,
			reason,
			expires_at,
			queue_position
		) VALUES (?, ?, ?, ?, '', ?, ?)
	`,
		input.MessageID,
		formatTime(now),
		string(model.DeliveryReceiptQueued),
		targetSessionID,
		formatTime(expiresAt),
		queuePosition,
	); err != nil {
		return AgentMessageRecord{}, fmt.Errorf("insert queued agent message event: %w", err)
	}

	record, err := loadAgentMessageRecordTx(ctx, tx, input.MessageID)
	if err != nil {
		return AgentMessageRecord{}, err
	}
	record.Message.TargetAnswerPublicKey = targetAnswerPublicKey

	if err := tx.Commit(); err != nil {
		return AgentMessageRecord{}, fmt.Errorf("commit queue agent message transaction: %w", err)
	}

	return record, nil
}

func (s *Store) PeekAgentInbox(
	ctx context.Context,
	sessionID string,
	now time.Time,
	limit int,
) (AgentSession, []AgentMessageRecord, error) {
	if s == nil || s.db == nil {
		return AgentSession{}, nil, errors.New("store is not initialized")
	}
	if strings.TrimSpace(sessionID) == "" {
		return AgentSession{}, nil, errors.New("session_id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureAgentSessionAnswerPublicKeyColumn(ctx); err != nil {
		return AgentSession{}, nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if limit <= 0 {
		limit = defaultInboxLimit
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentSession{}, nil, fmt.Errorf("begin inbox transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := expireQueuedAgentMessagesTx(ctx, tx, now); err != nil {
		return AgentSession{}, nil, err
	}

	session, err := loadAgentSessionRow(tx.QueryRowContext(ctx, `
		SELECT
			agent_id,
			installation_id,
			session_id,
			participant_id,
			capabilities_json,
			roles_json,
			answer_public_key,
			delivery_mode,
			session_status,
			lease_expires_at,
			host_alias,
			replaces_session_id,
			registered_at,
			last_seen_at
		FROM agent_sessions
		WHERE session_id = ?
	`, sessionID))
	if err != nil {
		return AgentSession{}, nil, err
	}
	if session.SessionStatus != model.AgentSessionOnline || !session.LeaseExpiresAt.After(now) {
		return AgentSession{}, nil, ErrAgentSessionNotFound
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT message_id
		FROM agent_messages
		WHERE target_participant_id = ? AND state = ?
		ORDER BY created_at ASC
	`, session.ParticipantID, string(model.DeliveryReceiptQueued))
	if err != nil {
		return AgentSession{}, nil, fmt.Errorf("query queued inbox messages: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	records := make([]AgentMessageRecord, 0, limit)
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			return AgentSession{}, nil, fmt.Errorf("scan queued inbox message: %w", err)
		}
		record, err := loadAgentMessageRecordTx(ctx, tx, messageID)
		if err != nil {
			return AgentSession{}, nil, err
		}
		if !channelAllowsSession(record.Channel, session) {
			continue
		}
		records = append(records, record)
		if len(records) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return AgentSession{}, nil, fmt.Errorf("iterate queued inbox messages: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return AgentSession{}, nil, fmt.Errorf("commit inbox transaction: %w", err)
	}

	return session, records, nil
}

func (s *Store) DeliverAgentMessage(
	ctx context.Context,
	messageID string,
	sessionID string,
	now time.Time,
) (AgentMessageRecord, error) {
	if s == nil || s.db == nil {
		return AgentMessageRecord{}, errors.New("store is not initialized")
	}
	if strings.TrimSpace(messageID) == "" {
		return AgentMessageRecord{}, errors.New("message_id is required")
	}
	if strings.TrimSpace(sessionID) == "" {
		return AgentMessageRecord{}, errors.New("session_id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureAgentSessionAnswerPublicKeyColumn(ctx); err != nil {
		return AgentMessageRecord{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentMessageRecord{}, fmt.Errorf("begin deliver agent message transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := expireQueuedAgentMessagesTx(ctx, tx, now); err != nil {
		return AgentMessageRecord{}, err
	}

	session, err := loadAgentSessionRow(tx.QueryRowContext(ctx, `
		SELECT
			agent_id,
			installation_id,
			session_id,
			participant_id,
			capabilities_json,
			roles_json,
			answer_public_key,
			delivery_mode,
			session_status,
			lease_expires_at,
			host_alias,
			replaces_session_id,
			registered_at,
			last_seen_at
		FROM agent_sessions
		WHERE session_id = ?
	`, sessionID))
	if err != nil {
		return AgentMessageRecord{}, err
	}
	if session.SessionStatus != model.AgentSessionOnline || !session.LeaseExpiresAt.After(now) {
		return AgentMessageRecord{}, ErrAgentSessionNotFound
	}

	record, err := loadAgentMessageRecordTx(ctx, tx, messageID)
	if err != nil {
		return AgentMessageRecord{}, err
	}
	switch record.Message.State {
	case model.DeliveryReceiptDelivered, model.DeliveryReceiptExpired, model.DeliveryReceiptRefused:
		return AgentMessageRecord{}, ErrAgentMessageFinalized
	}
	if record.Message.TargetParticipantID != session.ParticipantID {
		return AgentMessageRecord{}, ErrAgentMessageWrongQueue
	}
	if !channelAllowsSession(record.Channel, session) {
		return AgentMessageRecord{}, ErrAgentMessageNotFound
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_messages
		SET state = ?, delivered_session_id = ?, delivered_at = ?, reason = ''
		WHERE message_id = ?
	`,
		string(model.DeliveryReceiptDelivered),
		session.SessionID,
		formatTime(now),
		messageID,
	); err != nil {
		return AgentMessageRecord{}, fmt.Errorf("update delivered agent message: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_message_events (
			message_id,
			event_at,
			state,
			target_session_id,
			reason,
			expires_at,
			queue_position
		) VALUES (?, ?, ?, ?, '', '', 0)
	`,
		messageID,
		formatTime(now),
		string(model.DeliveryReceiptDelivered),
		session.SessionID,
	); err != nil {
		return AgentMessageRecord{}, fmt.Errorf("insert delivered agent message event: %w", err)
	}

	record, err = loadAgentMessageRecordTx(ctx, tx, messageID)
	if err != nil {
		return AgentMessageRecord{}, err
	}

	if err := tx.Commit(); err != nil {
		return AgentMessageRecord{}, fmt.Errorf("commit deliver agent message transaction: %w", err)
	}

	return record, nil
}

func (s *Store) GetAgentMessage(
	ctx context.Context,
	messageID string,
	now time.Time,
) (AgentMessageRecord, error) {
	if s == nil || s.db == nil {
		return AgentMessageRecord{}, errors.New("store is not initialized")
	}
	if strings.TrimSpace(messageID) == "" {
		return AgentMessageRecord{}, errors.New("message_id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentMessageRecord{}, fmt.Errorf("begin get agent message transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := expireQueuedAgentMessagesTx(ctx, tx, now); err != nil {
		return AgentMessageRecord{}, err
	}

	record, err := loadAgentMessageRecordTx(ctx, tx, messageID)
	if err != nil {
		return AgentMessageRecord{}, err
	}

	if err := tx.Commit(); err != nil {
		return AgentMessageRecord{}, fmt.Errorf("commit get agent message transaction: %w", err)
	}

	return record, nil
}

func expireQueuedAgentMessagesTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT message_id
		FROM agent_messages
		WHERE state = ? AND expires_at <= ?
	`, string(model.DeliveryReceiptQueued), formatTime(now))
	if err != nil {
		return fmt.Errorf("query expired agent messages: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var expiredIDs []string
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			return fmt.Errorf("scan expired agent message: %w", err)
		}
		expiredIDs = append(expiredIDs, messageID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate expired agent messages: %w", err)
	}

	for _, messageID := range expiredIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE agent_messages
			SET state = ?, reason = ?
			WHERE message_id = ?
		`, string(model.DeliveryReceiptExpired), "expired before delivery", messageID); err != nil {
			return fmt.Errorf("expire agent message: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_message_events (
				message_id,
				event_at,
				state,
				target_session_id,
				reason,
				expires_at,
				queue_position
			) VALUES (?, ?, ?, '', ?, '', 0)
		`,
			messageID,
			formatTime(now),
			string(model.DeliveryReceiptExpired),
			"expired before delivery",
		); err != nil {
			return fmt.Errorf("insert expired agent message event: %w", err)
		}
	}

	return nil
}

func queuedMessagePositionTx(ctx context.Context, tx *sql.Tx, targetParticipantID string) (int, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM agent_messages
		WHERE target_participant_id = ? AND state = ?
	`, targetParticipantID, string(model.DeliveryReceiptQueued))

	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count queued agent messages: %w", err)
	}
	return count, nil
}

func loadAgentSessionRow(row *sql.Row) (AgentSession, error) {
	return loadAgentSession(row)
}

type agentSessionScanner interface {
	Scan(dest ...any) error
}

func loadAgentSession(scanner agentSessionScanner) (AgentSession, error) {
	var session AgentSession
	var capabilities string
	var roles string
	var deliveryMode string
	var sessionStatus string
	var leaseExpiresAt string
	var registeredAt string
	var lastSeenAt string
	if err := scanner.Scan(
		&session.AgentID,
		&session.InstallationID,
		&session.SessionID,
		&session.ParticipantID,
		&capabilities,
		&roles,
		&session.AnswerPublicKey,
		&deliveryMode,
		&sessionStatus,
		&leaseExpiresAt,
		&session.HostAlias,
		&session.ReplacesSessionID,
		&registeredAt,
		&lastSeenAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentSession{}, ErrAgentSessionNotFound
		}
		return AgentSession{}, fmt.Errorf("scan agent session: %w", err)
	}
	session.Capabilities = splitCapabilities(capabilities)
	session.Roles = splitCapabilities(roles)
	session.DeliveryMode = model.AgentDeliveryMode(deliveryMode)
	session.SessionStatus = model.AgentSessionStatus(sessionStatus)
	session.LeaseExpiresAt = parseTime(leaseExpiresAt)
	session.RegisteredAt = parseTime(registeredAt)
	session.LastSeenAt = parseTime(lastSeenAt)
	return session, nil
}

func loadAgentMessageRecordTx(ctx context.Context, tx *sql.Tx, messageID string) (AgentMessageRecord, error) {
	var record AgentMessageRecord
	var createdAt string
	var expiresAt string
	var state string
	var deliveredAt string
	row := tx.QueryRowContext(ctx, `
		SELECT
			message_id,
			sender_session_id,
			sender_participant_id,
			target_participant_id,
			target_agent_id,
			target_answer_public_key,
			channel_id,
			body,
			created_at,
			expires_at,
			state,
			delivered_session_id,
			delivered_at,
			reason
		FROM agent_messages
		WHERE message_id = ?
	`, messageID)
	if err := row.Scan(
		&record.Message.MessageID,
		&record.Message.SenderSessionID,
		&record.Message.SenderParticipantID,
		&record.Message.TargetParticipantID,
		&record.Message.TargetAgentID,
		&record.Message.TargetAnswerPublicKey,
		&record.Message.ChannelID,
		&record.Message.Body,
		&createdAt,
		&expiresAt,
		&state,
		&record.Message.DeliveredSessionID,
		&deliveredAt,
		&record.Message.Reason,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentMessageRecord{}, ErrAgentMessageNotFound
		}
		return AgentMessageRecord{}, fmt.Errorf("scan agent message: %w", err)
	}
	record.Message.CreatedAt = parseTime(createdAt)
	record.Message.ExpiresAt = parseTime(expiresAt)
	record.Message.State = model.DeliveryReceiptState(state)
	record.Message.DeliveredAt = parseTime(deliveredAt)
	if strings.TrimSpace(record.Message.ChannelID) != "" {
		channel, err := loadChannelRow(tx.QueryRowContext(ctx, `
			SELECT
				channel_id,
				display_name,
				description,
				restricted,
				allowed_participants,
				allowed_agents,
				allowed_roles,
				created_at,
				updated_at
			FROM agent_channels
			WHERE channel_id = ?
		`, record.Message.ChannelID))
		if err != nil {
			return AgentMessageRecord{}, err
		}
		record.Channel = &channel
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT sequence, event_at, state, target_session_id, reason, expires_at, queue_position
		FROM agent_message_events
		WHERE message_id = ?
		ORDER BY sequence ASC
	`, messageID)
	if err != nil {
		return AgentMessageRecord{}, fmt.Errorf("query agent message events: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		var event AgentMessageEvent
		var eventAt string
		var expires string
		var state string
		if err := rows.Scan(
			&event.Sequence,
			&eventAt,
			&state,
			&event.TargetSessionID,
			&event.Reason,
			&expires,
			&event.QueuePosition,
		); err != nil {
			return AgentMessageRecord{}, fmt.Errorf("scan agent message event: %w", err)
		}
		event.MessageID = messageID
		event.EventAt = parseTime(eventAt)
		event.State = model.DeliveryReceiptState(state)
		event.ExpiresAt = parseTime(expires)
		record.Events = append(record.Events, event)
	}
	if err := rows.Err(); err != nil {
		return AgentMessageRecord{}, fmt.Errorf("iterate agent message events: %w", err)
	}

	return record, nil
}

func joinCapabilities(values []string) string {
	if len(values) == 0 {
		return ""
	}
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			trimmed = append(trimmed, value)
		}
	}
	return strings.Join(trimmed, "\n")
}

func splitCapabilities(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}

func loadChannelRow(row *sql.Row) (model.Channel, error) {
	var channel model.Channel
	var restricted int
	var allowedParticipants string
	var allowedAgents string
	var allowedRoles string
	var createdAt string
	var updatedAt string
	if err := row.Scan(
		&channel.ChannelID,
		&channel.DisplayName,
		&channel.Description,
		&restricted,
		&allowedParticipants,
		&allowedAgents,
		&allowedRoles,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Channel{}, ErrChannelNotFound
		}
		return model.Channel{}, fmt.Errorf("scan channel: %w", err)
	}
	channel.Restricted = restricted != 0
	channel.AllowedParticipants = splitCapabilities(allowedParticipants)
	channel.AllowedAgents = splitCapabilities(allowedAgents)
	channel.AllowedRoles = splitCapabilities(allowedRoles)
	channel.CreatedAt = parseTime(createdAt)
	channel.UpdatedAt = parseTime(updatedAt)
	return channel, nil
}

func channelAllowsSession(channel *model.Channel, session AgentSession) bool {
	if channel == nil || !channel.Restricted {
		return true
	}
	for _, participantID := range channel.AllowedParticipants {
		if participantID == session.ParticipantID {
			return true
		}
	}
	for _, agentID := range channel.AllowedAgents {
		if agentID == session.AgentID {
			return true
		}
	}
	for _, allowedRole := range channel.AllowedRoles {
		for _, sessionRole := range session.Roles {
			if allowedRole == sessionRole {
				return true
			}
		}
	}
	return false
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
