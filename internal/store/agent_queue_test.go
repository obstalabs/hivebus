package store

import (
	"errors"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
)

const sampleAgentAnswerPublicKey = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="

func TestRegisterAgentSessionReplacesPreviousOnlineSession(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	now := time.Date(2026, 4, 18, 1, 0, 0, 0, time.UTC)

	first, err := st.RegisterAgentSession(
		t.Context(),
		sampleAgentSessionPayload("sess_nullbot_old", "agent.field.nullbot", now.Add(15*time.Minute)),
		model.MessageTypeAgentSessionRegistered,
		now,
	)
	if err != nil {
		t.Fatalf("RegisterAgentSession(first) error = %v", err)
	}
	if first.SessionStatus != model.AgentSessionOnline {
		t.Fatalf("expected online session, got %q", first.SessionStatus)
	}

	second, err := st.RegisterAgentSession(
		t.Context(),
		sampleAgentSessionPayload("sess_nullbot_new", "agent.field.nullbot", now.Add(30*time.Minute)),
		model.MessageTypeAgentSessionRegistered,
		now.Add(1*time.Minute),
	)
	if err != nil {
		t.Fatalf("RegisterAgentSession(second) error = %v", err)
	}
	if second.SessionID != "sess_nullbot_new" {
		t.Fatalf("expected new session id, got %q", second.SessionID)
	}
	if second.AnswerPublicKey != sampleAgentAnswerPublicKey {
		t.Fatalf("answer_public_key = %q, want sample key", second.AnswerPublicKey)
	}

	if _, _, err := st.PeekAgentInbox(t.Context(), "sess_nullbot_old", now.Add(2*time.Minute), 10); !errors.Is(err, ErrAgentSessionNotFound) {
		t.Fatalf("PeekAgentInbox(old session) error = %v, want %v", err, ErrAgentSessionNotFound)
	}

	session, messages, err := st.PeekAgentInbox(t.Context(), "sess_nullbot_new", now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("PeekAgentInbox(new session) error = %v", err)
	}
	if session.SessionID != "sess_nullbot_new" {
		t.Fatalf("expected new session id, got %q", session.SessionID)
	}
	if session.AnswerPublicKey != sampleAgentAnswerPublicKey {
		t.Fatalf("peek answer_public_key = %q, want sample key", session.AnswerPublicKey)
	}
	if len(messages) != 0 {
		t.Fatalf("expected empty new inbox, got %d", len(messages))
	}
}

func TestQueuePeekDeliverAgentMessageLifecycle(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	now := time.Date(2026, 4, 18, 2, 0, 0, 0, time.UTC)
	if _, err := st.RegisterAgentSession(
		t.Context(),
		sampleAgentSessionPayload("sess_nullbot_001", "agent.field.nullbot", now.Add(30*time.Minute)),
		model.MessageTypeAgentSessionRegistered,
		now,
	); err != nil {
		t.Fatalf("RegisterAgentSession() error = %v", err)
	}

	record, err := st.QueueAgentMessage(t.Context(), AgentMessageInput{
		MessageID:           "msg_agent_001",
		SenderSessionID:     "sess_dispatch_001",
		SenderParticipantID: "agent.dispatch",
		TargetParticipantID: "agent.field.nullbot",
		Body:                "I finished the API. Please run the smoke tests next.",
		TTL:                 time.Hour,
	}, now.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("QueueAgentMessage() error = %v", err)
	}
	if record.Message.TargetAnswerPublicKey != sampleAgentAnswerPublicKey {
		t.Fatalf("target_answer_public_key = %q, want sample key", record.Message.TargetAnswerPublicKey)
	}
	if record.Message.State != model.DeliveryReceiptQueued {
		t.Fatalf("expected queued state, got %q", record.Message.State)
	}
	if len(record.Events) != 1 || record.Events[0].QueuePosition != 1 {
		t.Fatalf("expected single queued event, got %#v", record.Events)
	}

	session, inbox, err := st.PeekAgentInbox(t.Context(), "sess_nullbot_001", now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("PeekAgentInbox() error = %v", err)
	}
	if session.ParticipantID != "agent.field.nullbot" {
		t.Fatalf("expected participant agent.field.nullbot, got %q", session.ParticipantID)
	}
	if len(inbox) != 1 || inbox[0].Message.MessageID != "msg_agent_001" {
		t.Fatalf("unexpected inbox %#v", inbox)
	}

	delivered, err := st.DeliverAgentMessage(
		t.Context(),
		"msg_agent_001",
		"sess_nullbot_001",
		now.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatalf("DeliverAgentMessage() error = %v", err)
	}
	if delivered.Message.State != model.DeliveryReceiptDelivered {
		t.Fatalf("expected delivered state, got %q", delivered.Message.State)
	}
	if delivered.Message.DeliveredSessionID != "sess_nullbot_001" {
		t.Fatalf("expected delivered session, got %q", delivered.Message.DeliveredSessionID)
	}
	if len(delivered.Events) != 2 || delivered.Events[1].State != model.DeliveryReceiptDelivered {
		t.Fatalf("expected queued + delivered events, got %#v", delivered.Events)
	}
}

func TestListAgentSessionsReturnsFreshOnlineRoster(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	now := time.Date(2026, 4, 18, 2, 30, 0, 0, time.UTC)

	if _, err := st.RegisterAgentSession(
		t.Context(),
		sampleAgentSessionPayload("sess_nullbot_001", "agent.field.nullbot", now.Add(30*time.Minute)),
		model.MessageTypeAgentSessionRegistered,
		now,
	); err != nil {
		t.Fatalf("RegisterAgentSession(nullbot) error = %v", err)
	}
	if _, err := st.RegisterAgentSession(
		t.Context(),
		sampleAgentSessionPayload("sess_architect_001", "architect.agent", now.Add(30*time.Minute)),
		model.MessageTypeAgentSessionRegistered,
		now.Add(time.Minute),
	); err != nil {
		t.Fatalf("RegisterAgentSession(architect) error = %v", err)
	}
	if _, err := st.RegisterAgentSession(
		t.Context(),
		sampleAgentSessionPayload("sess_expired_001", "agent.field.expired", now.Add(-time.Minute)),
		model.MessageTypeAgentSessionRegistered,
		now,
	); err != nil {
		t.Fatalf("RegisterAgentSession(expired) error = %v", err)
	}

	all, err := st.ListAgentSessions(t.Context(), now.Add(2*time.Minute), "")
	if err != nil {
		t.Fatalf("ListAgentSessions(all) error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all sessions = %#v, want two fresh online sessions", all)
	}

	filtered, err := st.ListAgentSessions(t.Context(), now.Add(2*time.Minute), "agent.field.")
	if err != nil {
		t.Fatalf("ListAgentSessions(filtered) error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].ParticipantID != "agent.field.nullbot" {
		t.Fatalf("filtered sessions = %#v, want only agent.field.nullbot", filtered)
	}
}

func TestQueueAgentMessageExpiresBeforeDelivery(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	now := time.Date(2026, 4, 18, 3, 0, 0, 0, time.UTC)
	if _, err := st.RegisterAgentSession(
		t.Context(),
		sampleAgentSessionPayload("sess_nullbot_001", "agent.field.nullbot", now.Add(30*time.Minute)),
		model.MessageTypeAgentSessionRegistered,
		now,
	); err != nil {
		t.Fatalf("RegisterAgentSession() error = %v", err)
	}

	if _, err := st.QueueAgentMessage(t.Context(), AgentMessageInput{
		MessageID:           "msg_agent_expire",
		SenderSessionID:     "sess_dispatch_001",
		SenderParticipantID: "agent.dispatch",
		TargetParticipantID: "agent.field.nullbot",
		Body:                "Ping me when you are free.",
		TTL:                 2 * time.Minute,
	}, now.Add(1*time.Minute)); err != nil {
		t.Fatalf("QueueAgentMessage() error = %v", err)
	}

	_, inbox, err := st.PeekAgentInbox(t.Context(), "sess_nullbot_001", now.Add(5*time.Minute), 10)
	if err != nil {
		t.Fatalf("PeekAgentInbox() error = %v", err)
	}
	if len(inbox) != 0 {
		t.Fatalf("expected expired inbox to be empty, got %#v", inbox)
	}

	record, err := st.GetAgentMessage(t.Context(), "msg_agent_expire", now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("GetAgentMessage() error = %v", err)
	}
	if record.Message.State != model.DeliveryReceiptExpired {
		t.Fatalf("expected expired state, got %q", record.Message.State)
	}
	if len(record.Events) != 2 || record.Events[1].State != model.DeliveryReceiptExpired {
		t.Fatalf("expected expiry event, got %#v", record.Events)
	}
}

func TestPeekAgentInboxHidesRestrictedChannelFromUnauthorizedSession(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	now := time.Date(2026, 4, 18, 4, 0, 0, 0, time.UTC)

	channel, err := st.UpsertChannel(t.Context(), model.Channel{
		ChannelID:   "security-private",
		DisplayName: "Security Private",
		Restricted:  true,
		AllowedRoles: []string{
			"security",
		},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("UpsertChannel() error = %v", err)
	}
	if channel.ChannelID != "security-private" {
		t.Fatalf("expected stored channel, got %#v", channel)
	}

	if _, err := st.RegisterAgentSession(
		t.Context(),
		sampleAgentSessionPayload("sess_nullbot_public", "agent.field.nullbot", now.Add(30*time.Minute)),
		model.MessageTypeAgentSessionRegistered,
		now,
	); err != nil {
		t.Fatalf("RegisterAgentSession(public) error = %v", err)
	}

	if _, err := st.QueueAgentMessage(t.Context(), AgentMessageInput{
		MessageID:           "msg_agent_secret",
		SenderSessionID:     "sess_dispatch_001",
		SenderParticipantID: "agent.dispatch",
		TargetParticipantID: "agent.field.nullbot",
		ChannelID:           "security-private",
		Body:                "Private security findings.",
		TTL:                 time.Hour,
	}, now.Add(1*time.Minute)); err != nil {
		t.Fatalf("QueueAgentMessage() error = %v", err)
	}

	_, publicInbox, err := st.PeekAgentInbox(t.Context(), "sess_nullbot_public", now.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("PeekAgentInbox(public) error = %v", err)
	}
	if len(publicInbox) != 0 {
		t.Fatalf("expected hidden restricted inbox, got %#v", publicInbox)
	}

	securePayload := sampleAgentSessionPayload("sess_nullbot_secure", "agent.field.nullbot", now.Add(30*time.Minute))
	securePayload.Roles = []string{"security"}
	if _, err := st.RegisterAgentSession(
		t.Context(),
		securePayload,
		model.MessageTypeAgentSessionRegistered,
		now.Add(3*time.Minute),
	); err != nil {
		t.Fatalf("RegisterAgentSession(secure) error = %v", err)
	}

	_, secureInbox, err := st.PeekAgentInbox(t.Context(), "sess_nullbot_secure", now.Add(4*time.Minute), 10)
	if err != nil {
		t.Fatalf("PeekAgentInbox(secure) error = %v", err)
	}
	if len(secureInbox) != 1 || secureInbox[0].Message.ChannelID != "security-private" {
		t.Fatalf("expected secure inbox message, got %#v", secureInbox)
	}
}

func TestDeliverAgentMessageHidesRestrictedChannelFromUnauthorizedSession(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	now := time.Date(2026, 4, 18, 5, 0, 0, 0, time.UTC)

	if _, err := st.UpsertChannel(t.Context(), model.Channel{
		ChannelID:   "security-private",
		DisplayName: "Security Private",
		Restricted:  true,
		AllowedRoles: []string{
			"security",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertChannel() error = %v", err)
	}

	if _, err := st.RegisterAgentSession(
		t.Context(),
		sampleAgentSessionPayload("sess_nullbot_public", "agent.field.nullbot", now.Add(30*time.Minute)),
		model.MessageTypeAgentSessionRegistered,
		now,
	); err != nil {
		t.Fatalf("RegisterAgentSession(public) error = %v", err)
	}

	if _, err := st.QueueAgentMessage(t.Context(), AgentMessageInput{
		MessageID:           "msg_agent_secret_deliver",
		SenderSessionID:     "sess_dispatch_001",
		SenderParticipantID: "agent.dispatch",
		TargetParticipantID: "agent.field.nullbot",
		ChannelID:           "security-private",
		Body:                "Private delivery path.",
		TTL:                 time.Hour,
	}, now.Add(time.Minute)); err != nil {
		t.Fatalf("QueueAgentMessage() error = %v", err)
	}

	_, err := st.DeliverAgentMessage(
		t.Context(),
		"msg_agent_secret_deliver",
		"sess_nullbot_public",
		now.Add(2*time.Minute),
	)
	if !errors.Is(err, ErrAgentMessageNotFound) {
		t.Fatalf("DeliverAgentMessage() error = %v, want %v", err, ErrAgentMessageNotFound)
	}
}

func sampleAgentSessionPayload(sessionID string, participantID string, leaseUntil time.Time) model.AgentSessionPayload {
	return model.AgentSessionPayload{
		AgentID:         "nullbot-edge",
		InstallationID:  "install_nullbot_edge_001",
		SessionID:       sessionID,
		ParticipantID:   participantID,
		Capabilities:    []string{"evidence.collect", "clarification.reply"},
		AnswerPublicKey: sampleAgentAnswerPublicKey,
		DeliveryMode:    model.AgentDeliveryQueued,
		SessionStatus:   model.AgentSessionOnline,
		LeaseExpiresAt:  leaseUntil.Format(time.RFC3339),
		HostAlias:       "smokevm-arm64",
	}
}
