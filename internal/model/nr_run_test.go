package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNRRunLifecycleAcceptsPolicyDeniedAndFailedReceipts(t *testing.T) {
	t.Helper()

	denied := nrRunTestEnvelope(t, MessageTypeNRRunPolicyDenied, NRRunPayload{
		NeuroRouterRunID: "nr_run_001",
		SourceThreadID:   "thr_nr_001",
		ToolCallID:       "tool_call_001",
		ToolName:         "deploy",
		Policy: &NRRunPolicyResult{
			Decision: NRRunPolicyDenied,
			RuleRef:  "policy://tool/deploy",
			Reason:   "operator approval required",
		},
		OccurredAt: "2026-05-16T12:00:00Z",
		Redacted:   true,
	})
	if err := denied.ValidateNRRunLifecycle(); err != nil {
		t.Fatalf("ValidateNRRunLifecycle(policy denied) error = %v", err)
	}

	failed := nrRunTestEnvelope(t, MessageTypeNRRunFailed, NRRunPayload{
		NeuroRouterRunID: "nr_run_001",
		SourceThreadID:   "thr_nr_001",
		FailureReason:    "test command failed",
		OccurredAt:       "2026-05-16T12:01:00Z",
		Redacted:         true,
	})
	if err := failed.ValidateNRRunLifecycle(); err != nil {
		t.Fatalf("ValidateNRRunLifecycle(failed) error = %v", err)
	}
}

func TestNRRunLifecycleAcceptsMissingOptionalRefs(t *testing.T) {
	t.Helper()

	envelope := nrRunTestEnvelope(t, MessageTypeNRRunContextProjected, NRRunPayload{
		ContextBundleHash: "sha256:bbbbbbbb",
		NeuroRouterRunID:  "nr_run_001",
		SourceThreadID:    "thr_nr_001",
		OccurredAt:        "2026-05-16T12:00:00Z",
		Redacted:          true,
	})

	if err := envelope.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestNRRunLifecycleRejectsCopiedRawFields(t *testing.T) {
	t.Helper()

	envelope := nrRunTestEnvelope(t, MessageTypeNRRunCompleted, NRRunPayload{
		NeuroRouterRunID: "nr_run_001",
		SourceThreadID:   "thr_nr_001",
		RedactedOutput:   "completed",
		OccurredAt:       "2026-05-16T12:00:00Z",
		Redacted:         true,
	})
	envelope.Payload = json.RawMessage(`{
		"neurorouter_run_id":"nr_run_001",
		"source_thread_id":"thr_nr_001",
		"redacted_output_summary":"completed",
		"raw_model_transcript":"copied transcript",
		"occurred_at":"2026-05-16T12:00:00Z",
		"redacted":true
	}`)

	if err := envelope.Validate(); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Validate() error = %v, want unknown field", err)
	}
}

func TestNRRunLifecycleRequiresRedactedPayload(t *testing.T) {
	t.Helper()

	envelope := nrRunTestEnvelope(t, MessageTypeNRRunCompleted, NRRunPayload{
		NeuroRouterRunID: "nr_run_001",
		SourceThreadID:   "thr_nr_001",
		RedactedOutput:   "completed",
		OccurredAt:       "2026-05-16T12:00:00Z",
	})

	if err := envelope.Validate(); err == nil || !strings.Contains(err.Error(), "must be redacted") {
		t.Fatalf("Validate() error = %v, want redaction error", err)
	}
}

func nrRunTestEnvelope(t *testing.T, messageType MessageType, payload NRRunPayload) Envelope {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	sentAt := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)

	return Envelope{
		MessageID:      "msg_" + strings.ReplaceAll(string(messageType), ".", "_"),
		ThreadID:       "thr_nr_001",
		From:           "service.neurorouter",
		To:             []string{"service.hivebus"},
		Type:           messageType,
		Payload:        body,
		SentAt:         sentAt,
		IdempotencyKey: "idem_" + strings.ReplaceAll(string(messageType), ".", "_"),
		Trace: Trace{
			CorrelationID: "corr_thr_nr_001",
			Verified:      true,
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + strings.ReplaceAll(string(messageType), ".", "_"),
		},
	}
}
