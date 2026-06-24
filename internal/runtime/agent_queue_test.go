package runtime

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
	"github.com/obstalabs/hivebus/internal/store"
)

func TestAgentMessagingLifecycleOverHTTP(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	registerBody := marshalJSON(t, model.AgentSessionPayload{
		AgentID:         "nullbot-edge",
		InstallationID:  "install_nullbot_edge_001",
		SessionID:       "sess_nullbot_001",
		ParticipantID:   "agent.field.nullbot",
		Capabilities:    []string{"clarification.reply"},
		AnswerPublicKey: "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=",
		DeliveryMode:    model.AgentDeliveryQueued,
		SessionStatus:   model.AgentSessionOnline,
		LeaseExpiresAt:  time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
		HostAlias:       "smokevm-arm64",
	})
	registerReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/agents/sessions/register",
		bytes.NewReader(registerBody),
	)
	registerReq.Header.Set("Content-Type", "application/json")
	registerReq.Header.Set("Authorization", "Bearer worker-secret")
	registerRec := httptest.NewRecorder()
	handler.ServeHTTP(registerRec, registerReq)
	if registerRec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", registerRec.Code, registerRec.Body.String())
	}
	var registerResponse registerAgentSessionResponse
	if err := json.Unmarshal(registerRec.Body.Bytes(), &registerResponse); err != nil {
		t.Fatalf("Unmarshal(register) error = %v", err)
	}
	if registerResponse.Session.AnswerPublicKey != "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=" {
		t.Fatalf("register answer_public_key = %q, want declared key", registerResponse.Session.AnswerPublicKey)
	}

	sendBody := marshalJSON(t, sendAgentMessageRequest{
		MessageID:           "msg_agent_001",
		SenderSessionID:     "sess_dispatch_001",
		SenderParticipantID: "agent.dispatch",
		TargetParticipantID: "agent.field.nullbot",
		Body:                "I finished the API. Please run the smoke tests next.",
	})
	sendReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/agents/messages/send",
		bytes.NewReader(sendBody),
	)
	sendReq.Header.Set("Content-Type", "application/json")
	sendReq.Header.Set("Authorization", "Bearer operator-secret")
	sendRec := httptest.NewRecorder()
	handler.ServeHTTP(sendRec, sendReq)
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("send status = %d, body = %s", sendRec.Code, sendRec.Body.String())
	}
	var sendResponse sendAgentMessageResponse
	if err := json.Unmarshal(sendRec.Body.Bytes(), &sendResponse); err != nil {
		t.Fatalf("Unmarshal(send) error = %v", err)
	}
	if sendResponse.Message.TargetAnswerPublicKey != "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=" {
		t.Fatalf("send target_answer_public_key = %q, want declared key", sendResponse.Message.TargetAnswerPublicKey)
	}

	inboxReq := httptest.NewRequest(
		http.MethodGet,
		"/v0/agents/sessions/sess_nullbot_001/inbox",
		nil,
	)
	inboxReq.Header.Set("Authorization", "Bearer worker-secret")
	inboxRec := httptest.NewRecorder()
	handler.ServeHTTP(inboxRec, inboxReq)
	if inboxRec.Code != http.StatusOK {
		t.Fatalf("inbox status = %d, body = %s", inboxRec.Code, inboxRec.Body.String())
	}

	var inbox inboxResponse
	if err := json.Unmarshal(inboxRec.Body.Bytes(), &inbox); err != nil {
		t.Fatalf("Unmarshal(inbox) error = %v", err)
	}
	if len(inbox.Messages) != 1 || inbox.Messages[0].Message.MessageID != "msg_agent_001" {
		t.Fatalf("unexpected inbox payload %#v", inbox)
	}
	if inbox.Messages[0].Message.TargetAnswerPublicKey != "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=" {
		t.Fatalf("inbox target_answer_public_key = %q, want declared key", inbox.Messages[0].Message.TargetAnswerPublicKey)
	}

	deliverBody := marshalJSON(t, deliverAgentMessageRequest{SessionID: "sess_nullbot_001"})
	deliverReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/agents/messages/msg_agent_001/deliver",
		bytes.NewReader(deliverBody),
	)
	deliverReq.Header.Set("Content-Type", "application/json")
	deliverReq.Header.Set("Authorization", "Bearer worker-secret")
	deliverRec := httptest.NewRecorder()
	handler.ServeHTTP(deliverRec, deliverReq)
	if deliverRec.Code != http.StatusOK {
		t.Fatalf("deliver status = %d, body = %s", deliverRec.Code, deliverRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v0/agents/messages/msg_agent_001", nil)
	getReq.Header.Set("Authorization", "Bearer operator-secret")
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", getRec.Code, getRec.Body.String())
	}

	var record store.AgentMessageRecord
	if err := json.Unmarshal(getRec.Body.Bytes(), &record); err != nil {
		t.Fatalf("Unmarshal(record) error = %v", err)
	}
	if record.Message.State != model.DeliveryReceiptDelivered {
		t.Fatalf("expected delivered state, got %q", record.Message.State)
	}
	if len(record.Events) != 2 {
		t.Fatalf("expected queued + delivered events, got %#v", record.Events)
	}
}

func TestAgentMessagingAcceptsSignedAskQueryBodyOverHTTP(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	mustRegisterAgentSession(t, handler, "sess_asker_001", "architect/agent")
	mustRegisterAgentSession(t, handler, "sess_answerer_001", "workledger/agent")

	// WO-97: runtime send must accept the exact Body string emitted by live ask.
	queryBody := marshalJSON(t, mustSignedAgentAskEnvelope(t))
	sendBody := marshalJSON(t, sendAgentMessageRequest{
		MessageID:           "query-wo97",
		SenderSessionID:     "sess_asker_001",
		SenderParticipantID: "architect/agent",
		TargetParticipantID: "workledger/agent",
		Body:                string(queryBody),
	})
	sendReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/agents/messages/send",
		bytes.NewReader(sendBody),
	)
	sendReq.Header.Set("Content-Type", "application/json")
	sendReq.Header.Set("Authorization", "Bearer operator-secret")
	sendRec := httptest.NewRecorder()
	handler.ServeHTTP(sendRec, sendReq)
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("send status = %d, body = %s", sendRec.Code, sendRec.Body.String())
	}

	var sendResponse sendAgentMessageResponse
	if err := json.Unmarshal(sendRec.Body.Bytes(), &sendResponse); err != nil {
		t.Fatalf("Unmarshal(sendResponse) error = %v", err)
	}
	if sendResponse.Message.MessageID != "query-wo97" {
		t.Fatalf("send response message_id = %q, want query-wo97", sendResponse.Message.MessageID)
	}

	inboxReq := httptest.NewRequest(http.MethodGet, "/v0/agents/sessions/sess_answerer_001/inbox", nil)
	inboxReq.Header.Set("Authorization", "Bearer worker-secret")
	inboxRec := httptest.NewRecorder()
	handler.ServeHTTP(inboxRec, inboxReq)
	if inboxRec.Code != http.StatusOK {
		t.Fatalf("inbox status = %d, body = %s", inboxRec.Code, inboxRec.Body.String())
	}

	var inbox inboxResponse
	if err := json.Unmarshal(inboxRec.Body.Bytes(), &inbox); err != nil {
		t.Fatalf("Unmarshal(inbox) error = %v", err)
	}
	if len(inbox.Messages) != 1 {
		t.Fatalf("inbox messages = %#v, want one", inbox.Messages)
	}
	if inbox.Messages[0].Message.MessageID != "query-wo97" {
		t.Fatalf("inbox message_id = %q, want query-wo97", inbox.Messages[0].Message.MessageID)
	}
	if inbox.Messages[0].Message.Body != string(queryBody) {
		t.Fatalf("inbox body = %q, want signed query body", inbox.Messages[0].Message.Body)
	}
}

func TestAgentRosterOverHTTP(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	mustRegisterAgentSession(t, handler, "sess_nullbot_001", "agent.field.nullbot")
	mustRegisterAgentSession(t, handler, "sess_architect_001", "architect/agent")

	req := httptest.NewRequest(http.MethodGet, "/v0/agents/sessions?prefix=agent.field.", nil)
	req.Header.Set("Authorization", "Bearer worker-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("roster status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var roster rosterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &roster); err != nil {
		t.Fatalf("Unmarshal(roster) error = %v", err)
	}
	if roster.Status != "ok" {
		t.Fatalf("roster status field = %q, want ok", roster.Status)
	}
	if len(roster.Sessions) != 1 || roster.Sessions[0].ParticipantID != "agent.field.nullbot" {
		t.Fatalf("roster sessions = %#v, want only agent.field.nullbot", roster.Sessions)
	}
}

func TestAgentMessagingRequiresMatchingQueueOnDeliver(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	mustRegisterAgentSession(t, handler, "sess_nullbot_001", "agent.field.nullbot")
	mustRegisterAgentSession(t, handler, "sess_other_001", "agent.other")
	mustSendAgentMessage(t, handler, "msg_agent_conflict", "agent.field.nullbot", "")

	deliverBody := marshalJSON(t, deliverAgentMessageRequest{SessionID: "sess_other_001"})
	deliverReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/agents/messages/msg_agent_conflict/deliver",
		bytes.NewReader(deliverBody),
	)
	deliverReq.Header.Set("Content-Type", "application/json")
	deliverReq.Header.Set("Authorization", "Bearer worker-secret")
	deliverRec := httptest.NewRecorder()
	handler.ServeHTTP(deliverRec, deliverReq)
	if deliverRec.Code != http.StatusConflict {
		t.Fatalf("deliver conflict status = %d, body = %s", deliverRec.Code, deliverRec.Body.String())
	}
}

func TestRestrictedChannelHiddenFromUnauthorizedAgentOverHTTP(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	channelBody := marshalJSON(t, model.Channel{
		ChannelID:   "security-private",
		DisplayName: "Security Private",
		Restricted:  true,
		AllowedRoles: []string{
			"security",
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	channelReq := httptest.NewRequest(http.MethodPost, "/v0/channels", bytes.NewReader(channelBody))
	channelReq.Header.Set("Content-Type", "application/json")
	channelReq.Header.Set("Authorization", "Bearer operator-secret")
	channelRec := httptest.NewRecorder()
	handler.ServeHTTP(channelRec, channelReq)
	if channelRec.Code != http.StatusCreated {
		t.Fatalf("channel create status = %d, body = %s", channelRec.Code, channelRec.Body.String())
	}

	mustRegisterAgentSession(t, handler, "sess_nullbot_001", "agent.field.nullbot")
	mustSendAgentMessage(t, handler, "msg_agent_hidden", "agent.field.nullbot", "security-private")

	inboxReq := httptest.NewRequest(http.MethodGet, "/v0/agents/sessions/sess_nullbot_001/inbox", nil)
	inboxReq.Header.Set("Authorization", "Bearer worker-secret")
	inboxRec := httptest.NewRecorder()
	handler.ServeHTTP(inboxRec, inboxReq)
	if inboxRec.Code != http.StatusOK {
		t.Fatalf("hidden inbox status = %d, body = %s", inboxRec.Code, inboxRec.Body.String())
	}

	var hiddenInbox inboxResponse
	if err := json.Unmarshal(inboxRec.Body.Bytes(), &hiddenInbox); err != nil {
		t.Fatalf("Unmarshal(hiddenInbox) error = %v", err)
	}
	if len(hiddenInbox.Messages) != 0 {
		t.Fatalf("expected hidden restricted inbox, got %#v", hiddenInbox.Messages)
	}

	mustRegisterAgentSession(t, handler, "sess_nullbot_secure", "agent.field.nullbot", "security")

	secureReq := httptest.NewRequest(http.MethodGet, "/v0/agents/sessions/sess_nullbot_secure/inbox", nil)
	secureReq.Header.Set("Authorization", "Bearer worker-secret")
	secureRec := httptest.NewRecorder()
	handler.ServeHTTP(secureRec, secureReq)
	if secureRec.Code != http.StatusOK {
		t.Fatalf("secure inbox status = %d, body = %s", secureRec.Code, secureRec.Body.String())
	}

	var secureInbox inboxResponse
	if err := json.Unmarshal(secureRec.Body.Bytes(), &secureInbox); err != nil {
		t.Fatalf("Unmarshal(secureInbox) error = %v", err)
	}
	if len(secureInbox.Messages) != 1 || secureInbox.Messages[0].Message.ChannelID != "security-private" {
		t.Fatalf("expected secure restricted message, got %#v", secureInbox.Messages)
	}

	deliverBody := marshalJSON(t, deliverAgentMessageRequest{SessionID: "sess_nullbot_001"})
	deliverReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/agents/messages/msg_agent_hidden/deliver",
		bytes.NewReader(deliverBody),
	)
	deliverReq.Header.Set("Content-Type", "application/json")
	deliverReq.Header.Set("Authorization", "Bearer worker-secret")
	deliverRec := httptest.NewRecorder()
	handler.ServeHTTP(deliverRec, deliverReq)
	if deliverRec.Code != http.StatusNotFound {
		t.Fatalf("unauthorized deliver status = %d, body = %s", deliverRec.Code, deliverRec.Body.String())
	}
}

func TestChannelEndpointsRequireOperator(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	channelBody := marshalJSON(t, model.Channel{
		ChannelID:   "security-private",
		DisplayName: "Security Private",
		Restricted:  true,
		AllowedRoles: []string{
			"security",
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})

	createReq := httptest.NewRequest(http.MethodPost, "/v0/channels", bytes.NewReader(channelBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("Authorization", "Bearer worker-secret")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusForbidden {
		t.Fatalf("worker channel create status = %d, body = %s", createRec.Code, createRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v0/channels/security-private", nil)
	getReq.Header.Set("Authorization", "Bearer worker-secret")
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusForbidden {
		t.Fatalf("worker channel get status = %d, body = %s", getRec.Code, getRec.Body.String())
	}
}

func mustRegisterAgentSession(
	t *testing.T,
	handler http.Handler,
	sessionID string,
	participantID string,
	roles ...string,
) {
	t.Helper()

	registerBody := marshalJSON(t, model.AgentSessionPayload{
		AgentID:        "agent-" + participantID,
		InstallationID: "install-" + participantID,
		SessionID:      sessionID,
		ParticipantID:  participantID,
		Capabilities:   []string{"clarification.reply"},
		Roles:          roles,
		DeliveryMode:   model.AgentDeliveryQueued,
		SessionStatus:  model.AgentSessionOnline,
		LeaseExpiresAt: time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodPost, "/v0/agents/sessions/register", bytes.NewReader(registerBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer worker-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func mustSendAgentMessage(
	t *testing.T,
	handler http.Handler,
	messageID string,
	targetParticipantID string,
	channelID string,
) {
	t.Helper()

	sendBody := marshalJSON(t, sendAgentMessageRequest{
		MessageID:           messageID,
		SenderSessionID:     "sess_dispatch_001",
		SenderParticipantID: "agent.dispatch",
		TargetParticipantID: targetParticipantID,
		ChannelID:           channelID,
		Body:                "Heads up from another agent.",
	})
	req := httptest.NewRequest(http.MethodPost, "/v0/agents/messages/send", bytes.NewReader(sendBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func mustSignedAgentAskEnvelope(t *testing.T) model.Envelope {
	t.Helper()

	// WO-97: exercise runtime queueing with the same signed query envelope kind live ask sends.
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{97}, ed25519.SeedSize))
	query := model.Envelope{
		MessageID:      "query-wo97",
		ThreadID:       "thread-wo97",
		From:           "architect/agent",
		To:             []string{"workledger/agent"},
		Type:           model.MessageTypeQuery,
		Payload:        json.RawMessage(`{"question":"which workledger checkout is canonical?","question_type":"canonical_repo","read_only":true}`),
		SentAt:         time.Now().UTC(),
		IdempotencyKey: "idem-query-wo97",
		Trace: model.Trace{
			CorrelationID:   "corr-wo97",
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed,
		},
		Security: model.Security{
			Scheme: model.SecuritySchemeEd25519,
			Nonce:  "nonce-wo97",
		},
	}

	signed, err := model.SignEnvelope(query, privateKey)
	if err != nil {
		t.Fatalf("SignEnvelope(query) error = %v", err)
	}

	return signed
}
