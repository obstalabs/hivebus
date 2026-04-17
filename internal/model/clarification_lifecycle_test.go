package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestClarificationReceiptPayloadValidateAcceptsQueuedReceipt(t *testing.T) {
	t.Helper()

	payload := ClarificationReceiptPayload{
		RequestMessageID: "msg_002",
		State:            ClarificationReceiptQueued,
		QueuePosition:    1,
	}

	if err := payload.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestClarificationOutcomePayloadValidateRejectsMissingFailureState(t *testing.T) {
	t.Helper()

	payload := ClarificationOutcomePayload{
		RequestMessageID: "msg_002",
		Outcome:          ClarificationOutcomeNeedsHuman,
		MaxRounds:        3,
		MaxEvidenceBytes: 8192,
		RoundsUsed:       3,
		EvidenceBytes:    2048,
	}

	if err := payload.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestEnvelopeValidateClarificationLifecycleAcceptsOutcome(t *testing.T) {
	t.Helper()

	body, err := json.Marshal(ClarificationOutcomePayload{
		RequestMessageID: "msg_002",
		Outcome:          ClarificationOutcomeNeedsHuman,
		FailureState:     ClarificationFailureMaxRounds,
		MaxRounds:        3,
		MaxEvidenceBytes: 8192,
		RoundsUsed:       3,
		EvidenceBytes:    3072,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	envelope := Envelope{
		MessageID:      "msg_005",
		ThreadID:       "thr_prod_api_502",
		From:           "service.hivebus",
		To:             []string{"collector.nullbot"},
		Type:           MessageTypeClarifyOutcome,
		Payload:        body,
		SentAt:         time.Date(2026, 4, 17, 8, 30, 0, 0, time.UTC),
		IdempotencyKey: "idem_msg_005",
		Trace: Trace{
			CorrelationID: "corr_thr_prod_api_502",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_msg_005",
		},
	}

	if err := envelope.ValidateClarificationLifecycle(); err != nil {
		t.Fatalf("ValidateClarificationLifecycle() error = %v", err)
	}
}
