package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEnvelopeValidateAcceptsSignedStructuredEnvelope(t *testing.T) {
	t.Helper()

	sentAt := time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC)
	deadline := sentAt.Add(2 * time.Hour)
	payload, err := json.Marshal(map[string]string{
		"issue": "postgres latency spike",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	envelope := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Capability:     "incident.diagnose",
		Payload:        payload,
		Deadline:       &deadline,
		SentAt:         sentAt,
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
			Verified:      true,
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
			Signed: true,
		},
	}

	if err := envelope.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestEnvelopeValidateRejectsInvalidPayload(t *testing.T) {
	t.Helper()

	envelope := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage("{"),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}

	if err := envelope.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestEnvelopeValidateRejectsDuplicateRecipients(t *testing.T) {
	t.Helper()

	envelope := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator", "agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}

	if err := envelope.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestArtifactValidateRejectsNegativeSize(t *testing.T) {
	t.Helper()

	artifact := Artifact{
		ArtifactID: "art_1",
		Name:       "log.txt",
		Kind:       "log",
		URI:        "s3://example/log.txt",
		SHA256:     "abc123",
		SizeBytes:  -1,
	}

	if err := artifact.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestEnvelopeValidateRejectsMissingRequiredFields(t *testing.T) {
	t.Helper()

	base := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		SentAt:         time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}

	testCases := []Envelope{
		func() Envelope {
			sample := base
			sample.MessageID = ""
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.Type = MessageType("weird")
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.To = nil
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.SentAt = time.Time{}
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.IdempotencyKey = ""
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.Trace.CorrelationID = ""
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.Security.Scheme = ""
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.Security.Nonce = ""
			return sample
		}(),
		func() Envelope {
			sample := base
			sample.To = []string{""}
			return sample
		}(),
	}

	for _, envelope := range testCases {
		if err := envelope.Validate(); err == nil {
			t.Fatalf("Validate() expected an error for %#v", envelope)
		}
	}
}

func TestEnvelopeValidateRejectsDeadlineBeforeSentAt(t *testing.T) {
	t.Helper()

	sentAt := time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC)
	deadline := sentAt.Add(-1 * time.Minute)

	envelope := Envelope{
		MessageID:      "msg_123",
		ThreadID:       "thr_9",
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"db down"}`),
		Deadline:       &deadline,
		SentAt:         sentAt,
		IdempotencyKey: "idem_123",
		Trace: Trace{
			CorrelationID: "corr_123",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}

	if err := envelope.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestArtifactValidateRejectsMissingRequiredFields(t *testing.T) {
	t.Helper()

	testCases := []Artifact{
		{Name: "log.txt", Kind: "log", URI: "s3://example/log.txt", SHA256: "abc123"},
		{ArtifactID: "art_1", Kind: "log", URI: "s3://example/log.txt", SHA256: "abc123"},
		{ArtifactID: "art_1", Name: "log.txt", URI: "s3://example/log.txt", SHA256: "abc123"},
		{ArtifactID: "art_1", Name: "log.txt", Kind: "log", SHA256: "abc123"},
		{ArtifactID: "art_1", Name: "log.txt", Kind: "log", URI: "s3://example/log.txt"},
	}

	for _, artifact := range testCases {
		if err := artifact.Validate(); err == nil {
			t.Fatalf("Validate() expected an error for %#v", artifact)
		}
	}
}
