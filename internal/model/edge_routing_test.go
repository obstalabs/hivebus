package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAgentSessionPayloadValidateAcceptsRegistration(t *testing.T) {
	t.Helper()

	payload := sampleAgentSessionPayload()
	if err := payload.Validate(MessageTypeAgentSessionRegistered); err != nil {
		t.Fatalf("Validate(registered) error = %v", err)
	}
}

func TestAgentSessionPayloadValidateRejectsBadReplacementState(t *testing.T) {
	t.Helper()

	payload := sampleAgentSessionPayload()
	payload.ReplacesSessionID = "sess_old"

	if err := payload.Validate(MessageTypeAgentSessionRegistered); err == nil {
		t.Fatal("Validate(registered) expected an error")
	}
}

func TestDeliveryReceiptPayloadValidateAcceptsQueuedReceipt(t *testing.T) {
	t.Helper()

	payload := DeliveryReceiptPayload{
		TargetParticipantID: "agent.field.nullbot",
		TargetAgentID:       "nullbot-edge",
		State:               DeliveryReceiptQueued,
		ExpiresAt:           "2026-04-18T06:00:00Z",
		QueuePosition:       1,
	}
	if err := payload.Validate(); err != nil {
		t.Fatalf("Validate(queued) error = %v", err)
	}
}

func TestEnvelopeValidateEdgeRoutingAcceptsDeliveredReceipt(t *testing.T) {
	t.Helper()

	body, err := json.Marshal(DeliveryReceiptPayload{
		TargetParticipantID: "agent.field.nullbot",
		TargetAgentID:       "nullbot-edge",
		TargetSessionID:     "sess_nullbot_001",
		State:               DeliveryReceiptDelivered,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	envelope := Envelope{
		MessageID:      "msg_receipt_delivered",
		ThreadID:       "thr_nullbot_reply",
		From:           "service.hivebus",
		To:             []string{"agent.dispatch"},
		Type:           MessageTypeAgentDeliveryReceipt,
		Payload:        body,
		SentAt:         time.Date(2026, 4, 17, 8, 2, 0, 0, time.UTC),
		IdempotencyKey: "idem_receipt_delivered",
		Trace: Trace{
			CorrelationID: "corr_thr_nullbot_reply",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_receipt_delivered",
		},
	}

	if err := envelope.ValidateEdgeRouting(); err != nil {
		t.Fatalf("ValidateEdgeRouting() error = %v", err)
	}
}

func sampleAgentSessionPayload() AgentSessionPayload {
	return AgentSessionPayload{
		AgentID:        "nullbot-edge",
		InstallationID: "install_nullbot_edge_001",
		SessionID:      "sess_nullbot_001",
		ParticipantID:  "agent.field.nullbot",
		Capabilities:   []string{"evidence.collect", "clarification.reply"},
		DeliveryMode:   AgentDeliveryQueued,
		SessionStatus:  AgentSessionOnline,
		LeaseExpiresAt: "2026-04-17T09:00:00Z",
		HostAlias:      "smokevm-arm64",
	}
}
