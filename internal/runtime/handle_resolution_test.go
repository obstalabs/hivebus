package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
	"github.com/obstalabs/hivebus/internal/store"
)

// registerHandleSession registers a session with a stable handle/repository and
// an explicit lease, so a test can make one participant stale and another live.
func registerHandleSession(t *testing.T, handler http.Handler, sessionID, participantID, handle, repository string, leaseExpires time.Time) {
	t.Helper()
	body := marshalJSON(t, model.AgentSessionPayload{
		AgentID:        "agent-" + participantID,
		InstallationID: "install-" + participantID,
		SessionID:      sessionID,
		ParticipantID:  participantID,
		Capabilities:   []string{"clarification.reply"},
		Handle:         handle,
		Repository:     repository,
		DeliveryMode:   model.AgentDeliveryQueued,
		SessionStatus:  model.AgentSessionOnline,
		LeaseExpiresAt: leaseExpires.UTC().Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodPost, "/v0/agents/sessions/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer worker-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %s status = %d, body = %s", sessionID, rec.Code, rec.Body.String())
	}
}

func sendByHandle(t *testing.T, handler http.Handler, req sendAgentMessageRequest) *httptest.ResponseRecorder {
	t.Helper()
	body := marshalJSON(t, req)
	httpReq := httptest.NewRequest(http.MethodPost, "/v0/agents/messages/send", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httpReq)
	return rec
}

// TestSendToHandleRoutesFreshestLiveParticipantOverHTTP is the WO-174 ghost-kill
// anchor at the HTTP send boundary: a stale p1 and a live p2 share the handle;
// a send to target_handle=architect must route to p2, stamp resolution
// provenance, and record the stale client hint as ignored.
func TestSendToHandleRoutesFreshestLiveParticipantOverHTTP(t *testing.T) {
	st := openTestStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), mustTestKeyStore(t))

	now := time.Now().UTC()
	registerHandleSession(t, handler, "sess_p1", "participant-p1", "architect", "neurorouter-pro", now.Add(-2*time.Minute))
	registerHandleSession(t, handler, "sess_p2", "participant-p2", "architect", "neurorouter-pro", now.Add(30*time.Minute))

	rec := sendByHandle(t, handler, sendAgentMessageRequest{
		MessageID:           "msg_handle_001",
		SenderSessionID:     "sess_dispatch",
		SenderParticipantID: "agent.dispatch",
		TargetParticipantID: "participant-p1", // stale cached hint that must be ignored
		TargetHandle:        "architect",
		Repository:          "neurorouter-pro",
		Body:                "resolve me to the live one",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp sendAgentMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal(send) error = %v", err)
	}
	if resp.Message.TargetParticipantID != "participant-p2" {
		t.Fatalf("target_participant_id = %q, want participant-p2 (live, not stale p1)", resp.Message.TargetParticipantID)
	}
	if resp.Message.ResolvedTargetParticipantID != "participant-p2" || resp.Message.ResolvedTargetSessionID != "sess_p2" {
		t.Fatalf("resolution metadata = %+v, want p2/sess_p2", resp.Message)
	}
	if resp.Message.ResolutionMode != store.ResolutionModeServerSideHandle {
		t.Fatalf("resolution_mode = %q, want server_side_handle", resp.Message.ResolutionMode)
	}
	if resp.Message.IgnoredTargetParticipantID != "participant-p1" {
		t.Fatalf("ignored_target_participant_id = %q, want participant-p1", resp.Message.IgnoredTargetParticipantID)
	}
	assertHandleResolutionProvenance(t, resp.Message)

	// The message landed in the live participant's inbox (p1 is stale/expired
	// and un-peekable, so p2 holding exactly the one message proves the route).
	getRecord := getAgentMessage(t, handler, "msg_handle_001")
	assertHandleResolutionProvenance(t, getRecord.Message)

	if got := inboxCount(t, handler, "sess_p2"); got != 1 {
		t.Fatalf("live p2 inbox count = %d, want 1", got)
	}
	inbox := peekAgentInbox(t, handler, "sess_p2")
	if len(inbox.Messages) != 1 {
		t.Fatalf("live p2 inbox count = %d, want 1", len(inbox.Messages))
	}
	assertHandleResolutionProvenance(t, inbox.Messages[0].Message)

	delivered := deliverAgentMessage(t, handler, "msg_handle_001", "sess_p2")
	assertHandleResolutionProvenance(t, delivered.Message)
}

func TestHeartbeatOmittingHandleStillAllowsHandleSendOverHTTP(t *testing.T) {
	st := openTestStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), mustTestKeyStore(t))
	now := time.Now().UTC()

	registerHandleSession(t, handler, "sess_live", "participant-live", "architect", "neurorouter-pro", now.Add(30*time.Minute))

	// WO-176: older heartbeat clients may omit the optional route key; that must
	// not clear an already registered handle/repository.
	heartbeatBody := marshalJSON(t, model.AgentSessionPayload{
		AgentID:        "agent-participant-live",
		InstallationID: "install-participant-live",
		SessionID:      "sess_live",
		ParticipantID:  "participant-live",
		Capabilities:   []string{"clarification.reply"},
		DeliveryMode:   model.AgentDeliveryQueued,
		SessionStatus:  model.AgentSessionOnline,
		LeaseExpiresAt: now.Add(30 * time.Minute).UTC().Format(time.RFC3339),
	})
	heartbeatReq := httptest.NewRequest(http.MethodPost, "/v0/agents/sessions/heartbeat", bytes.NewReader(heartbeatBody))
	heartbeatReq.Header.Set("Content-Type", "application/json")
	heartbeatReq.Header.Set("Authorization", "Bearer worker-secret")
	heartbeatRec := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatRec, heartbeatReq)
	if heartbeatRec.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, body = %s", heartbeatRec.Code, heartbeatRec.Body.String())
	}

	rec := sendByHandle(t, handler, sendAgentMessageRequest{
		MessageID:           "msg_after_heartbeat",
		SenderSessionID:     "sess_dispatch",
		SenderParticipantID: "agent.dispatch",
		TargetHandle:        "architect",
		Repository:          "neurorouter-pro",
		Body:                "route key survived heartbeat",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp sendAgentMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal(send) error = %v", err)
	}
	if resp.Message.TargetParticipantID != "participant-live" {
		t.Fatalf("target_participant_id = %q, want participant-live", resp.Message.TargetParticipantID)
	}
}

// TestChannelSendIgnoresTargetHandleRouting pins the directed-only boundary: a
// send carrying a channel_id is a broadcast and MUST NOT resolve the handle,
// even if one is present.
func TestChannelSendIgnoresTargetHandleRouting(t *testing.T) {
	st := openTestStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), mustTestKeyStore(t))

	now := time.Now().UTC()
	registerHandleSession(t, handler, "sess_live", "participant-live", "architect", "", now.Add(30*time.Minute))

	// Create the channel the send targets.
	channelBody := marshalJSON(t, model.Channel{ChannelID: "room-1", DisplayName: "Room 1"})
	channelReq := httptest.NewRequest(http.MethodPost, "/v0/channels", bytes.NewReader(channelBody))
	channelReq.Header.Set("Content-Type", "application/json")
	channelReq.Header.Set("Authorization", "Bearer operator-secret")
	channelRec := httptest.NewRecorder()
	handler.ServeHTTP(channelRec, channelReq)
	if channelRec.Code != http.StatusCreated {
		t.Fatalf("channel status = %d, body = %s", channelRec.Code, channelRec.Body.String())
	}

	rec := sendByHandle(t, handler, sendAgentMessageRequest{
		MessageID:           "msg_channel_001",
		SenderSessionID:     "sess_dispatch",
		SenderParticipantID: "agent.dispatch",
		TargetParticipantID: "participant-live",
		TargetHandle:        "architect", // present, but a channel send must ignore it for routing
		ChannelID:           "room-1",
		Body:                "broadcast, do not resolve",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("channel send status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp sendAgentMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal(send) error = %v", err)
	}
	if resp.Message.ResolutionMode != "" || resp.Message.ResolvedTargetParticipantID != "" {
		t.Fatalf("channel send resolved a handle: %+v", resp.Message)
	}
}

// TestSendToHandleNotLiveReturns409 pins the failure code when a handle exists
// but no session is currently live.
func TestSendToHandleNotLiveReturns409(t *testing.T) {
	st := openTestStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), mustTestKeyStore(t))

	now := time.Now().UTC()
	registerHandleSession(t, handler, "sess_stale", "participant-stale", "architect", "", now.Add(-1*time.Minute))

	rec := sendByHandle(t, handler, sendAgentMessageRequest{
		MessageID:           "msg_notlive",
		SenderSessionID:     "sess_dispatch",
		SenderParticipantID: "agent.dispatch",
		TargetHandle:        "architect",
		Body:                "nobody live",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("send status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
}

func inboxCount(t *testing.T, handler http.Handler, sessionID string) int {
	t.Helper()
	inbox := peekAgentInbox(t, handler, sessionID)
	return len(inbox.Messages)
}

func getAgentMessage(t *testing.T, handler http.Handler, messageID string) store.AgentMessageRecord {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v0/agents/messages/"+messageID, nil)
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get message %s status = %d, body = %s", messageID, rec.Code, rec.Body.String())
	}
	var record store.AgentMessageRecord
	if err := json.Unmarshal(rec.Body.Bytes(), &record); err != nil {
		t.Fatalf("Unmarshal(get message %s) error = %v", messageID, err)
	}
	return record
}

func peekAgentInbox(t *testing.T, handler http.Handler, sessionID string) inboxResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v0/agents/sessions/"+sessionID+"/inbox", nil)
	req.Header.Set("Authorization", "Bearer worker-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("inbox %s status = %d, body = %s", sessionID, rec.Code, rec.Body.String())
	}
	var inbox inboxResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &inbox); err != nil {
		t.Fatalf("Unmarshal(inbox %s) error = %v", sessionID, err)
	}
	return inbox
}

func deliverAgentMessage(t *testing.T, handler http.Handler, messageID, sessionID string) deliverAgentMessageResponse {
	t.Helper()
	body := marshalJSON(t, deliverAgentMessageRequest{SessionID: sessionID})
	req := httptest.NewRequest(http.MethodPost, "/v0/agents/messages/"+messageID+"/deliver", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer worker-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deliver message %s status = %d, body = %s", messageID, rec.Code, rec.Body.String())
	}
	var resp deliverAgentMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal(deliver message %s) error = %v", messageID, err)
	}
	return resp
}

func assertHandleResolutionProvenance(t *testing.T, message store.AgentMessage) {
	t.Helper()
	if message.TargetHandle != "architect" {
		t.Fatalf("target_handle = %q, want architect", message.TargetHandle)
	}
	if message.TargetRepository != "neurorouter-pro" {
		t.Fatalf("target_repository = %q, want neurorouter-pro", message.TargetRepository)
	}
	if message.ResolvedTargetParticipantID != "participant-p2" {
		t.Fatalf("resolved_target_participant_id = %q, want participant-p2", message.ResolvedTargetParticipantID)
	}
	if message.ResolvedTargetSessionID != "sess_p2" {
		t.Fatalf("resolved_target_session_id = %q, want sess_p2", message.ResolvedTargetSessionID)
	}
	if message.ResolutionMode != store.ResolutionModeServerSideHandle {
		t.Fatalf("resolution_mode = %q, want %q", message.ResolutionMode, store.ResolutionModeServerSideHandle)
	}
	if message.IgnoredTargetParticipantID != "participant-p1" {
		t.Fatalf("ignored_target_participant_id = %q, want participant-p1", message.IgnoredTargetParticipantID)
	}
}
