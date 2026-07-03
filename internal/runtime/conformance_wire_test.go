package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/conformance"
	"github.com/obstalabs/hivebus/internal/model"
	"github.com/obstalabs/hivebus/internal/store"
)

// TestInternalTypesMatchConformanceContract pins that Hivebus's REAL internal
// wire types serialize identically to the public conformance contract. The
// conformance package's own test proves the mirror structs match the golden
// fixtures; this test proves the INTERNAL structs match the same fixtures — so a
// sampled-field change in internal/runtime or internal/model that diverges from
// the shared contract fails here, on the Hivebus side, exactly as it would fail
// on a consumer side. Together they close the non-additive drift failure class.
//
// On an intentional wire change: update the internal struct AND regenerate the
// golden (UPDATE_GOLDEN=1 go test ./conformance/...) AND bump conformance.Version.
func TestInternalTypesMatchConformanceContract(t *testing.T) {
	// Register / heartbeat share model.AgentSessionPayload. Build the internal
	// value with the SAME field values as conformance.Sample(register).
	sessionPayload := conformanceAgentSessionPayload()

	sendRequest := sendAgentMessageRequest{
		MessageID:           "hbm-1",
		SenderSessionID:     "nr-session-1",
		SenderParticipantID: "nr-participant-1",
		TargetParticipantID: "nr-participant-2",
		Body:                "what work order are you on?",
		TTLSeconds:          600,
	}

	deliverRequest := deliverAgentMessageRequest{SessionID: "nr-session-2"}

	// WO-174: pin the resolved-by-handle message shape (ghost-kill anchor). The
	// REAL store.AgentMessage with resolution provenance must serialize exactly
	// like conformance.Sample(RouteMessageSendHandle).
	sentByHandle := store.AgentMessage{
		MessageID:                   "hbm-2",
		SenderSessionID:             "nr-session-1",
		SenderParticipantID:         "nr-participant-1",
		TargetParticipantID:         "nr-participant-2",
		TargetHandle:                "architect",
		TargetRepository:            "neurorouter-pro",
		ResolvedTargetParticipantID: "nr-participant-2",
		ResolvedTargetSessionID:     "nr-session-2",
		ResolutionMode:              store.ResolutionModeServerSideHandle,
		IgnoredTargetParticipantID:  "nr-participant-1",
		Body:                        "which work order are you on?",
		CreatedAt:                   time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC),
		ExpiresAt:                   time.Date(2026, 1, 1, 0, 10, 30, 0, time.UTC),
		State:                       model.DeliveryReceiptQueued,
	}

	// WO-159: pin the runtime inbox response body, not only path addressing.
	inbox := inboxResponse{
		Status:  "ok",
		Session: conformanceAgentSession(),
		Messages: []store.AgentMessageRecord{
			{
				Message: store.AgentMessage{
					MessageID:             "hbm-1",
					SenderSessionID:       "nr-session-2",
					SenderParticipantID:   "nr-participant-2",
					TargetParticipantID:   "nr-participant-1",
					TargetAgentID:         "claude/hivebus",
					TargetAnswerPublicKey: "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=",
					Body:                  "what work order are you on?",
					CreatedAt:             time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC),
					ExpiresAt:             time.Date(2026, 1, 1, 0, 10, 30, 0, time.UTC),
					State:                 model.DeliveryReceiptQueued,
				},
				Events: []store.AgentMessageEvent{
					{
						Sequence:        1,
						MessageID:       "hbm-1",
						EventAt:         time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC),
						State:           model.DeliveryReceiptQueued,
						TargetSessionID: "nr-session-1",
						ExpiresAt:       time.Date(2026, 1, 1, 0, 10, 30, 0, time.UTC),
						QueuePosition:   1,
					},
				},
			},
		},
	}

	checks := []struct {
		route conformance.Route
		value any
	}{
		{conformance.RouteSessionRegister, sessionPayload},
		{conformance.RouteSessionHeartbeat, sessionPayload},
		{conformance.RouteMessageSend, sendRequest},
		{conformance.RouteMessageSendHandle, sentByHandle},
		{conformance.RouteMessageDeliver, deliverRequest},
		{conformance.RouteInbox, inbox},
	}

	for _, c := range checks {
		got, err := json.Marshal(c.value)
		if err != nil {
			t.Fatalf("%s: marshal internal type: %v", c.route, err)
		}
		if err := conformance.CompareBytes(c.route, got); err != nil {
			t.Errorf("internal type drifted from contract: %v", err)
		}
	}
}

func TestRuntimeInboxHTTPResponseMatchesConformanceContract(t *testing.T) {
	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	handler := NewHandlerWithOptions(st, openTestArtifactStore(t), keys, HandlerOptions{
		Now: func() time.Time { return now },
	})

	postJSON := func(method, path string, role Role, value any) *httptest.ResponseRecorder {
		t.Helper()
		body := marshalJSON(t, value)
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", conformanceAuthHeader(role))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	registerRec := postJSON(http.MethodPost, "/v0/agents/sessions/register", RoleWorker, conformanceInboxAgentSessionPayload())
	if registerRec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", registerRec.Code, registerRec.Body.String())
	}

	now = time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC)
	sendRec := postJSON(http.MethodPost, "/v0/agents/messages/send", RoleOperator, sendAgentMessageRequest{
		MessageID:           "hbm-1",
		SenderSessionID:     "nr-session-2",
		SenderParticipantID: "nr-participant-2",
		TargetParticipantID: "nr-participant-1",
		Body:                "what work order are you on?",
		TTLSeconds:          600,
	})
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("send status = %d, body = %s", sendRec.Code, sendRec.Body.String())
	}

	// WO-161: the real inbox route must match the public fixture after store reload.
	now = time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)
	heartbeatRec := postJSON(http.MethodPost, "/v0/agents/sessions/heartbeat", RoleWorker, conformanceInboxAgentSessionPayload())
	if heartbeatRec.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d, body = %s", heartbeatRec.Code, heartbeatRec.Body.String())
	}

	inboxReq := httptest.NewRequest(http.MethodGet, "/v0/agents/sessions/nr-session-1/inbox", nil)
	inboxReq.Header.Set("Authorization", conformanceAuthHeader(RoleWorker))
	inboxRec := httptest.NewRecorder()
	handler.ServeHTTP(inboxRec, inboxReq)
	if inboxRec.Code != http.StatusOK {
		t.Fatalf("inbox status = %d, body = %s", inboxRec.Code, inboxRec.Body.String())
	}
	if err := conformance.CompareBytes(conformance.RouteInbox, inboxRec.Body.Bytes()); err != nil {
		t.Fatalf("real HTTP inbox response drifted from conformance fixture: %v\nbody: %s", err, inboxRec.Body.String())
	}
}

func conformanceAuthHeader(role Role) string {
	switch role {
	case RoleOperator:
		return "Bearer operator-secret"
	default:
		return "Bearer worker-secret"
	}
}

func conformanceAgentSessionPayload() model.AgentSessionPayload {
	payload := conformanceInboxAgentSessionPayload()
	payload.Handle = "architect"           // WO-177: public register/heartbeat route key.
	payload.Repository = "neurorouter-pro" // WO-177: optional route-key scope.
	return payload
}

func conformanceInboxAgentSessionPayload() model.AgentSessionPayload {
	return model.AgentSessionPayload{
		AgentID:         "claude/hivebus",
		InstallationID:  "install-1",
		SessionID:       "nr-session-1",
		ParticipantID:   "nr-participant-1",
		Capabilities:    []string{"repo_status", "canonical_worktree_status"},
		Roles:           []string{"worker"},
		AnswerPublicKey: "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=",
		DeliveryMode:    model.AgentDeliveryMode("queued_delivery"),
		SessionStatus:   model.AgentSessionStatus("online"),
		LeaseExpiresAt:  "2026-01-01T00:02:00Z",
		HostAlias:       "host-a",
	}
}

func conformanceAgentSession() store.AgentSession {
	return store.AgentSession{
		AgentID:         "claude/hivebus",
		InstallationID:  "install-1",
		SessionID:       "nr-session-1",
		ParticipantID:   "nr-participant-1",
		Capabilities:    []string{"repo_status", "canonical_worktree_status"},
		Roles:           []string{"worker"},
		AnswerPublicKey: "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=",
		DeliveryMode:    model.AgentDeliveryMode("queued_delivery"),
		SessionStatus:   model.AgentSessionStatus("online"),
		LeaseExpiresAt:  time.Date(2026, 1, 1, 0, 2, 0, 0, time.UTC),
		HostAlias:       "host-a",
		RegisteredAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		LastSeenAt:      time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC),
	}
}
