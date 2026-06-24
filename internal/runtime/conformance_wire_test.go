package runtime

import (
	"encoding/json"
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
// field change in internal/runtime or internal/model that diverges from the
// shared contract fails here, on the Hivebus side, exactly as it would fail on a
// consumer side. Together they close the drift failure class.
//
// On an intentional wire change: update the internal struct AND regenerate the
// golden (UPDATE_GOLDEN=1 go test ./conformance/...) AND bump conformance.Version.
func TestInternalTypesMatchConformanceContract(t *testing.T) {
	// Register / heartbeat share model.AgentSessionPayload. Build the internal
	// value with the SAME field values as conformance.Sample(register).
	sessionPayload := model.AgentSessionPayload{
		AgentID:         "claude/hivebus",
		InstallationID:  "install-1",
		SessionID:       "nr-session-1",
		ParticipantID:   "nr-participant-1",
		Capabilities:    []string{"repo_status", "canonical_worktree_status"},
		Roles:           []string{"worker"},
		AnswerPublicKey: "ed25519:AAAA",
		DeliveryMode:    model.AgentDeliveryMode("queued_delivery"),
		SessionStatus:   model.AgentSessionStatus("online"),
		LeaseExpiresAt:  "2026-01-01T00:02:00Z",
		HostAlias:       "host-a",
	}

	sendRequest := sendAgentMessageRequest{
		MessageID:           "hbm-1",
		SenderSessionID:     "nr-session-1",
		SenderParticipantID: "nr-participant-1",
		TargetParticipantID: "nr-participant-2",
		Body:                "what work order are you on?",
		TTLSeconds:          600,
	}

	deliverRequest := deliverAgentMessageRequest{SessionID: "nr-session-2"}
	roster := rosterResponse{
		Status: "ok",
		Sessions: []store.AgentSession{
			{
				AgentID:         "claude/hivebus",
				InstallationID:  "install-1",
				SessionID:       "nr-session-1",
				ParticipantID:   "nr-participant-1",
				Capabilities:    []string{"repo_status", "canonical_worktree_status"},
				Roles:           []string{"worker"},
				AnswerPublicKey: "ed25519:AAAA",
				DeliveryMode:    model.AgentDeliveryMode("queued_delivery"),
				SessionStatus:   model.AgentSessionStatus("online"),
				LeaseExpiresAt:  time.Date(2026, 1, 1, 0, 2, 0, 0, time.UTC),
				HostAlias:       "host-a",
				RegisteredAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				LastSeenAt:      time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC),
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
		{conformance.RouteMessageDeliver, deliverRequest},
		{conformance.RouteRoster, roster},
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
