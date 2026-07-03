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

// ResolutionModeServerSideHandle marks a message whose target participant was
// resolved server-side from a stable handle at send time. WO-174.
const ResolutionModeServerSideHandle = "server_side_handle"

// WO-175: message provenance columns that must survive store reloads.
var agentMessageResolutionColumns = []string{
	"target_handle",
	"target_repository",
	"resolved_target_participant_id",
	"resolved_target_session_id",
	"resolution_mode",
	"ignored_target_participant_id",
}

var (
	ErrAgentSessionNotFound   = errors.New("agent session not found")
	ErrAgentMessageNotFound   = errors.New("agent message not found")
	ErrAgentMessageFinalized  = errors.New("agent message is already finalized")
	ErrAgentMessageWrongQueue = errors.New("agent message does not belong to the requesting session")

	// WO-174: server-side handle-resolution outcomes. The runtime maps these to
	// the wire error codes target_handle_not_found (404), target_handle_not_live
	// and target_handle_ambiguous (409).
	ErrTargetHandleNotFound  = errors.New("target_handle_not_found")
	ErrTargetHandleNotLive   = errors.New("target_handle_not_live")
	ErrTargetHandleAmbiguous = errors.New("target_handle_ambiguous")
)

// ResolvedTargetHandle is the outcome of resolving a stable handle to the
// freshest-live participant. WO-174.
type ResolvedTargetHandle struct {
	ParticipantID string
	SessionID     string
	StaleMatches  int
}

type AgentSession struct {
	AgentID           string                   `json:"agent_id"`
	InstallationID    string                   `json:"installation_id"`
	SessionID         string                   `json:"session_id"`
	ParticipantID     string                   `json:"participant_id"`
	Capabilities      []string                 `json:"capabilities,omitempty"`
	Roles             []string                 `json:"roles,omitempty"`
	AnswerPublicKey   string                   `json:"answer_public_key,omitempty"` // WO-122: session discovery key; trust stays with askers.
	Handle            string                   `json:"handle,omitempty"`            // WO-174: stable logical route key.
	Repository        string                   `json:"repository,omitempty"`        // WO-174: optional scope for role handles.
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
	TargetHandle        string `json:"target_handle,omitempty"` // WO-174: stable route key; resolved server-side to a live participant.
	Repository          string `json:"repository,omitempty"`    // WO-174: optional scope for role handles.
	// WO-175: server-side resolution provenance is persisted with the queued message.
	ResolvedTargetParticipantID string `json:"resolved_target_participant_id,omitempty"`
	ResolvedTargetSessionID     string `json:"resolved_target_session_id,omitempty"`
	ResolutionMode              string `json:"resolution_mode,omitempty"`
	IgnoredTargetParticipantID  string `json:"ignored_target_participant_id,omitempty"`
	ChannelID                   string `json:"channel_id,omitempty"`
	Body                        string `json:"body"`
	TTL                         time.Duration
}

type AgentMessage struct {
	MessageID             string `json:"message_id"`
	SenderSessionID       string `json:"sender_session_id"`
	SenderParticipantID   string `json:"sender_participant_id"`
	TargetParticipantID   string `json:"target_participant_id"`
	TargetAgentID         string `json:"target_agent_id,omitempty"`
	TargetAnswerPublicKey string `json:"target_answer_public_key,omitempty"` // WO-122: send response exposes the target session's declared key.
	// WO-174: server-side handle resolution provenance. Present only when a
	// directed send targeted a stable handle and the broker resolved it.
	TargetHandle                string                     `json:"target_handle,omitempty"`
	TargetRepository            string                     `json:"target_repository,omitempty"`
	ResolvedTargetParticipantID string                     `json:"resolved_target_participant_id,omitempty"`
	ResolvedTargetSessionID     string                     `json:"resolved_target_session_id,omitempty"`
	ResolutionMode              string                     `json:"resolution_mode,omitempty"`
	IgnoredTargetParticipantID  string                     `json:"ignored_target_participant_id,omitempty"`
	ChannelID                   string                     `json:"channel_id,omitempty"`
	Body                        string                     `json:"body"`
	CreatedAt                   time.Time                  `json:"created_at"`
	ExpiresAt                   time.Time                  `json:"expires_at"`
	State                       model.DeliveryReceiptState `json:"state"`
	DeliveredSessionID          string                     `json:"delivered_session_id,omitempty"`
	DeliveredAt                 time.Time                  `json:"delivered_at,omitempty"`
	Reason                      string                     `json:"reason,omitempty"`
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

	present := make(map[string]struct{})
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
		present[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate agent_sessions schema: %w", err)
	}

	// WO-174: keep handle/repository on the lazy path alongside answer_public_key
	// so queue/inbox operations never SELECT a column a partially-migrated DB lacks.
	for _, column := range []string{"answer_public_key", "handle", "repository"} {
		if _, ok := present[column]; ok {
			continue
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(
			`ALTER TABLE agent_sessions ADD COLUMN %s TEXT NOT NULL DEFAULT ''`, column,
		)); err != nil {
			return fmt.Errorf("add %s to agent_sessions: %w", column, err)
		}
	}

	return nil
}

func (s *Store) ensureAgentMessageResolutionColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(agent_messages)`)
	if err != nil {
		return fmt.Errorf("inspect agent_messages schema: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	present := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("scan agent_messages schema: %w", err)
		}
		present[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate agent_messages schema: %w", err)
	}

	for _, column := range agentMessageResolutionColumns {
		if _, ok := present[column]; ok {
			continue
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(
			`ALTER TABLE agent_messages ADD COLUMN %s TEXT NOT NULL DEFAULT ''`, column,
		)); err != nil {
			return fmt.Errorf("add %s to agent_messages: %w", column, err)
		}
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

	// WO-176: omitted heartbeat route keys preserve the existing handle/repository.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_sessions (
			agent_id,
			installation_id,
			session_id,
			participant_id,
			capabilities_json,
			roles_json,
			answer_public_key,
			handle,
			repository,
			delivery_mode,
			session_status,
			lease_expires_at,
			host_alias,
			replaces_session_id,
			registered_at,
			last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			agent_id = excluded.agent_id,
			installation_id = excluded.installation_id,
			participant_id = excluded.participant_id,
			capabilities_json = excluded.capabilities_json,
			roles_json = excluded.roles_json,
			answer_public_key = excluded.answer_public_key,
			handle = CASE
				WHEN excluded.handle = '' THEN agent_sessions.handle
				ELSE excluded.handle
			END,
			repository = CASE
				WHEN excluded.repository = '' THEN agent_sessions.repository
				ELSE excluded.repository
			END,
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
		strings.TrimSpace(payload.Handle),
		strings.TrimSpace(payload.Repository),
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
			handle,
			repository,
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
	if err := s.ensureAgentMessageResolutionColumns(ctx); err != nil {
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
			target_handle,
			target_repository,
			resolved_target_participant_id,
			resolved_target_session_id,
			resolution_mode,
			ignored_target_participant_id,
			channel_id,
			body,
			created_at,
			expires_at,
			state,
			delivered_session_id,
			delivered_at,
			reason
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', '')
	`,
		input.MessageID,
		input.SenderSessionID,
		input.SenderParticipantID,
		input.TargetParticipantID,
		targetAgentID,
		targetAnswerPublicKey,
		strings.TrimSpace(input.TargetHandle),
		strings.TrimSpace(input.Repository),
		strings.TrimSpace(input.ResolvedTargetParticipantID),
		strings.TrimSpace(input.ResolvedTargetSessionID),
		strings.TrimSpace(input.ResolutionMode),
		strings.TrimSpace(input.IgnoredTargetParticipantID),
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

// ResolveTargetHandle maps a stable logical handle (+ optional repository scope)
// to the freshest-live participant, ARP-style: the sender addresses the handle,
// the store resolves the current live participant at send time. WO-174.
//
// OSS scope note: liveness is session_status='online' AND lease_expires_at>now,
// and among live matches the freshest is chosen by last_seen_at. OSS does NOT
// carry logical-origin/presence-signature provenance, so it intentionally omits
// the supersession + trust-rank tiebreak layers a provenance-bearing consumer
// applies; last_seen_at ordering is the deterministic OSS approximation.
func (s *Store) ResolveTargetHandle(
	ctx context.Context,
	handle string,
	repository string,
	now time.Time,
) (ResolvedTargetHandle, error) {
	if s == nil || s.db == nil {
		return ResolvedTargetHandle{}, errors.New("store is not initialized")
	}
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return ResolvedTargetHandle{}, ErrTargetHandleNotFound
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.ensureAgentSessionAnswerPublicKeyColumn(ctx); err != nil {
		return ResolvedTargetHandle{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	repository = strings.TrimSpace(repository)

	// A #-suffixed handle names one exact session key and must fail closed: it
	// never broadens to role/repo matching, and multiple live rows are never
	// ambiguous (the newest wins). WO-174.
	suffixed := strings.Contains(handle, "#")

	args := []any{handle}
	query := `
		SELECT participant_id, session_id, session_status, lease_expires_at
		FROM agent_sessions
		WHERE handle = ?`
	if repository != "" {
		query += ` AND repository = ?`
		args = append(args, repository)
	}
	query += ` ORDER BY last_seen_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return ResolvedTargetHandle{}, fmt.Errorf("resolve target handle: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var live []ResolvedTargetHandle
	staleMatches := 0
	totalMatches := 0
	for rows.Next() {
		var participantID, sessionID, sessionStatus, leaseExpiresAt string
		if err := rows.Scan(&participantID, &sessionID, &sessionStatus, &leaseExpiresAt); err != nil {
			return ResolvedTargetHandle{}, fmt.Errorf("scan target handle candidate: %w", err)
		}
		totalMatches++
		isLive := model.AgentSessionStatus(sessionStatus) == model.AgentSessionOnline &&
			now.Before(parseTime(leaseExpiresAt))
		if isLive {
			live = append(live, ResolvedTargetHandle{ParticipantID: participantID, SessionID: sessionID})
		} else {
			staleMatches++
		}
	}
	if err := rows.Err(); err != nil {
		return ResolvedTargetHandle{}, fmt.Errorf("iterate target handle candidates: %w", err)
	}

	switch {
	case totalMatches == 0:
		return ResolvedTargetHandle{}, ErrTargetHandleNotFound
	case len(live) == 0:
		return ResolvedTargetHandle{StaleMatches: staleMatches}, ErrTargetHandleNotLive
	case len(live) > 1 && !suffixed:
		// A broad selector matching multiple live participants cannot be routed
		// safely; a #-suffixed selector deterministically takes the newest.
		return ResolvedTargetHandle{}, ErrTargetHandleAmbiguous
	}

	// live is already ordered by last_seen_at DESC, so the head is freshest.
	resolved := live[0]
	resolved.StaleMatches = staleMatches
	return resolved, nil
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
	if err := s.ensureAgentMessageResolutionColumns(ctx); err != nil {
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
			handle,
			repository,
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
	if err := s.ensureAgentMessageResolutionColumns(ctx); err != nil {
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
			handle,
			repository,
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
	if err := s.ensureAgentMessageResolutionColumns(ctx); err != nil {
		return AgentMessageRecord{}, err
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
		&session.Handle,
		&session.Repository,
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
			target_handle,
			target_repository,
			resolved_target_participant_id,
			resolved_target_session_id,
			resolution_mode,
			ignored_target_participant_id,
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
		&record.Message.TargetHandle,
		&record.Message.TargetRepository,
		&record.Message.ResolvedTargetParticipantID,
		&record.Message.ResolvedTargetSessionID,
		&record.Message.ResolutionMode,
		&record.Message.IgnoredTargetParticipantID,
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
