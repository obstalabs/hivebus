package policy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
)

func TestValidateEnvelopeRejectsTierOverflow(t *testing.T) {
	t.Helper()

	payload, err := json.Marshal(map[string]string{
		"issue": "api unavailable",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	envelope := model.Envelope{
		MessageID:      "msg_1",
		ThreadID:       "thr_1",
		From:           "collector.nullbot",
		To:             []string{"a", "b", "c", "d", "e"},
		Type:           model.MessageTypeTaskRequest,
		Payload:        payload,
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_1",
		Trace: model.Trace{
			CorrelationID: "corr_1",
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_1",
			Signed: true,
		},
	}

	if err := ValidateEnvelope(model.TierFree, envelope, nil); err == nil {
		t.Fatal("ValidateEnvelope() expected an error")
	}
}

func TestValidateEnvelopeAcceptsEnterprisePolicy(t *testing.T) {
	t.Helper()

	payload, err := json.Marshal(map[string]string{
		"issue": "api unavailable",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	envelope := model.Envelope{
		MessageID:      "msg_1",
		ThreadID:       "thr_1",
		From:           "collector.nullbot",
		To:             []string{"agent.dispatch"},
		Type:           model.MessageTypeTaskRequest,
		Payload:        payload,
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_1",
		Trace: model.Trace{
			CorrelationID: "corr_1",
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_1",
			Signed: true,
		},
	}

	artifacts := []model.Artifact{
		{
			ArtifactID:  "art_1",
			Name:        "logs.txt",
			Kind:        "log",
			URI:         "artifact://sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			SizeBytes:   1024,
			ContentType: "text/plain",
		},
	}

	if err := ValidateEnvelope(model.TierEnterprise, envelope, artifacts); err != nil {
		t.Fatalf("ValidateEnvelope() error = %v", err)
	}
}

func TestValidateEnvelopeRejectsUnsignedEnvelope(t *testing.T) {
	t.Helper()

	payload, err := json.Marshal(map[string]string{
		"issue": "api unavailable",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	envelope := model.Envelope{
		MessageID:      "msg_1",
		ThreadID:       "thr_1",
		From:           "collector.nullbot",
		To:             []string{"agent.dispatch"},
		Type:           model.MessageTypeTaskRequest,
		Payload:        payload,
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_1",
		Trace: model.Trace{
			CorrelationID: "corr_1",
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_1",
		},
	}

	if err := ValidateEnvelope(model.TierPro, envelope, nil); err == nil {
		t.Fatal("ValidateEnvelope() expected an error")
	}
}

func TestLimitsForReturnsConfiguredTierPolicy(t *testing.T) {
	t.Helper()

	limits := LimitsFor(model.TierTeams)

	if limits.MaxRecipients != 48 {
		t.Fatalf("expected 48 recipients, got %d", limits.MaxRecipients)
	}
}

func TestValidateEnvelopeRejectsUnverifiedTrace(t *testing.T) {
	t.Helper()

	payload, err := json.Marshal(map[string]string{
		"issue": "api unavailable",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	envelope := model.Envelope{
		MessageID:      "msg_1",
		ThreadID:       "thr_1",
		From:           "collector.nullbot",
		To:             []string{"agent.dispatch"},
		Type:           model.MessageTypeTaskRequest,
		Payload:        payload,
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_1",
		Trace: model.Trace{
			CorrelationID: "corr_1",
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_1",
			Signed: true,
		},
	}

	if err := ValidateEnvelope(model.TierTeams, envelope, nil); err == nil {
		t.Fatal("ValidateEnvelope() expected an error")
	}
}

func TestValidateEnvelopeRejectsUnknownTier(t *testing.T) {
	t.Helper()

	payload, err := json.Marshal(map[string]string{
		"issue": "api unavailable",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	envelope := model.Envelope{
		MessageID:      "msg_1",
		ThreadID:       "thr_1",
		From:           "collector.nullbot",
		To:             []string{"agent.dispatch"},
		Type:           model.MessageTypeTaskRequest,
		Payload:        payload,
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_1",
		Trace: model.Trace{
			CorrelationID: "corr_1",
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_1",
			Signed: true,
		},
	}

	if err := ValidateEnvelope(model.Tier("odd"), envelope, nil); err == nil {
		t.Fatal("ValidateEnvelope() expected an error")
	}
}
