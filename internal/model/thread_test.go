package model

import (
	"testing"
	"time"
)

func TestThreadTransitionAllowsInvestigationFlow(t *testing.T) {
	t.Helper()

	thread := Thread{
		ThreadID:     "thr_123",
		Title:        "Latency in production",
		Status:       ThreadStatusReported,
		CustomerTier: TierPro,
		Source:       "nullbot",
		Participants: []Participant{
			{ID: "collector.nullbot", Kind: ParticipantCollector},
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
	}

	if err := thread.Transition(ThreadStatusCollecting, thread.UpdatedAt.Add(1*time.Minute)); err != nil {
		t.Fatalf("Transition() collecting error = %v", err)
	}

	if err := thread.Transition(ThreadStatusInvestigating, thread.UpdatedAt.Add(1*time.Minute)); err != nil {
		t.Fatalf("Transition() investigating error = %v", err)
	}

	if thread.Status != ThreadStatusInvestigating {
		t.Fatalf("expected status %q, got %q", ThreadStatusInvestigating, thread.Status)
	}
}

func TestThreadTransitionRejectsSkippingToDone(t *testing.T) {
	t.Helper()

	thread := Thread{
		ThreadID:     "thr_123",
		Title:        "Latency in production",
		Status:       ThreadStatusReported,
		CustomerTier: TierPro,
		Source:       "nullbot",
		Participants: []Participant{
			{ID: "collector.nullbot", Kind: ParticipantCollector},
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
	}

	if err := thread.Transition(ThreadStatusDone, thread.UpdatedAt.Add(1*time.Minute)); err == nil {
		t.Fatal("Transition() expected an error")
	}
}

func TestThreadValidateRejectsDuplicateParticipants(t *testing.T) {
	t.Helper()

	thread := Thread{
		ThreadID:     "thr_123",
		Title:        "Latency in production",
		Status:       ThreadStatusReported,
		CustomerTier: TierPro,
		Source:       "nullbot",
		Participants: []Participant{
			{ID: "collector.nullbot", Kind: ParticipantCollector},
			{ID: "collector.nullbot", Kind: ParticipantCollector},
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
	}

	if err := thread.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestThreadTransitionAllowsStatusTouch(t *testing.T) {
	t.Helper()

	thread := Thread{
		ThreadID:     "thr_123",
		Title:        "Latency in production",
		Status:       ThreadStatusInvestigating,
		CustomerTier: TierPro,
		Source:       "nullbot",
		Participants: []Participant{
			{ID: "collector.nullbot", Kind: ParticipantCollector},
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC),
	}

	nextUpdate := thread.UpdatedAt.Add(1 * time.Minute)

	if err := thread.Transition(ThreadStatusInvestigating, nextUpdate); err != nil {
		t.Fatalf("Transition() error = %v", err)
	}

	if thread.UpdatedAt != nextUpdate {
		t.Fatalf("expected updated_at %v, got %v", nextUpdate, thread.UpdatedAt)
	}
}

func TestThreadValidateRejectsMissingRequiredFields(t *testing.T) {
	t.Helper()

	base := Thread{
		ThreadID:     "thr_123",
		Title:        "Latency in production",
		Status:       ThreadStatusReported,
		CustomerTier: TierPro,
		Source:       "nullbot",
		Participants: []Participant{
			{ID: "collector.nullbot", Kind: ParticipantCollector},
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
	}

	testCases := []Thread{
		func() Thread {
			sample := base
			sample.Title = ""
			return sample
		}(),
		func() Thread {
			sample := base
			sample.Status = ThreadStatus("strange")
			return sample
		}(),
		func() Thread {
			sample := base
			sample.CustomerTier = Tier("odd")
			return sample
		}(),
		func() Thread {
			sample := base
			sample.Source = ""
			return sample
		}(),
		func() Thread {
			sample := base
			sample.Participants = nil
			return sample
		}(),
		func() Thread {
			sample := base
			sample.CreatedAt = time.Time{}
			return sample
		}(),
		func() Thread {
			sample := base
			sample.UpdatedAt = sample.CreatedAt.Add(-1 * time.Minute)
			return sample
		}(),
		func() Thread {
			sample := base
			sample.Participants = []Participant{{Kind: ParticipantCollector}}
			return sample
		}(),
		func() Thread {
			sample := base
			sample.Evidence = []Artifact{{
				ArtifactID:  "art_1",
				Name:        "log.txt",
				Kind:        "log",
				URI:         "s3://example/log.txt",
				SHA256:      "short-digest",
				ContentType: "text/plain",
			}}
			return sample
		}(),
		func() Thread {
			sample := base
			sample.Evidence = []Artifact{
				{
					ArtifactID:  "art_1",
					Name:        "log.txt",
					Kind:        "log",
					URI:         "s3://example/log.txt",
					SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					SizeBytes:   1,
					ContentType: "text/plain",
				},
				{
					ArtifactID:  "art_1",
					Name:        "other.txt",
					Kind:        "log",
					URI:         "s3://example/other.txt",
					SHA256:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					SizeBytes:   2,
					ContentType: "text/plain",
				},
			}
			return sample
		}(),
	}

	for _, thread := range testCases {
		if err := thread.Validate(); err == nil {
			t.Fatalf("Validate() expected an error for %#v", thread)
		}
	}
}

func TestThreadTransitionRejectsInvalidStateChanges(t *testing.T) {
	t.Helper()

	thread := Thread{
		ThreadID:     "thr_123",
		Title:        "Latency in production",
		Status:       ThreadStatusInvestigating,
		CustomerTier: TierPro,
		Source:       "nullbot",
		Participants: []Participant{
			{ID: "collector.nullbot", Kind: ParticipantCollector},
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC),
	}

	if err := thread.Transition(ThreadStatus("strange"), thread.UpdatedAt.Add(1*time.Minute)); err == nil {
		t.Fatal("Transition() expected an error for invalid next status")
	}

	if err := thread.Transition(ThreadStatusWaiting, time.Time{}); err == nil {
		t.Fatal("Transition() expected an error for zero timestamp")
	}

	if err := thread.Transition(ThreadStatusWaiting, thread.UpdatedAt.Add(-1*time.Minute)); err == nil {
		t.Fatal("Transition() expected an error for backwards timestamp")
	}
}

func TestPendingClarificationTracksOpenRequest(t *testing.T) {
	t.Helper()

	thread := Thread{
		ThreadID:     "thr_123",
		Title:        "Latency in production",
		Status:       ThreadStatusInvestigating,
		CustomerTier: TierPro,
		Source:       "nullbot",
		Participants: []Participant{
			{ID: "collector.nullbot", Kind: ParticipantCollector},
			{ID: "worker.smokevm", Kind: ParticipantAgent},
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
	}

	task := Envelope{
		MessageID:      "msg_task",
		ThreadID:       thread.ThreadID,
		From:           "collector.nullbot",
		To:             []string{"worker.smokevm"},
		Type:           MessageTypeTaskRequest,
		Payload:        []byte(`{"issue":"latency"}`),
		SentAt:         thread.CreatedAt.Add(1 * time.Minute),
		IdempotencyKey: "idem_task",
		Trace:          Trace{CorrelationID: "corr_123"},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_task",
		},
	}
	deadline := task.SentAt.Add(5 * time.Minute)
	clarification := Envelope{
		MessageID:      "msg_clarify",
		ThreadID:       thread.ThreadID,
		From:           "worker.smokevm",
		To:             []string{"collector.nullbot"},
		Type:           MessageTypeClarifyRequest,
		Payload:        []byte(`{"question":"which env?"}`),
		ReplyTo:        task.MessageID,
		Deadline:       &deadline,
		SentAt:         task.SentAt.Add(1 * time.Minute),
		IdempotencyKey: "idem_clarify",
		Trace:          Trace{CorrelationID: "corr_123"},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_clarify",
		},
	}

	state, err := thread.PendingClarification([]Envelope{task, clarification}, task.SentAt.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("PendingClarification() error = %v", err)
	}
	if state == nil {
		t.Fatal("expected pending clarification state")
	}
	if state.TaskMessageID != task.MessageID {
		t.Fatalf("expected task message %q, got %q", task.MessageID, state.TaskMessageID)
	}
	if state.Expired {
		t.Fatal("expected clarification to still be active")
	}
}

func TestPendingClarificationClearsAfterResponse(t *testing.T) {
	t.Helper()

	thread := Thread{
		ThreadID:     "thr_123",
		Title:        "Latency in production",
		Status:       ThreadStatusInvestigating,
		CustomerTier: TierPro,
		Source:       "nullbot",
		Participants: []Participant{
			{ID: "collector.nullbot", Kind: ParticipantCollector},
			{ID: "worker.smokevm", Kind: ParticipantAgent},
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
	}

	task := Envelope{
		MessageID:      "msg_task",
		ThreadID:       thread.ThreadID,
		From:           "collector.nullbot",
		To:             []string{"worker.smokevm"},
		Type:           MessageTypeTaskRequest,
		Payload:        []byte(`{"issue":"latency"}`),
		SentAt:         thread.CreatedAt.Add(1 * time.Minute),
		IdempotencyKey: "idem_task",
		Trace:          Trace{CorrelationID: "corr_123"},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_task",
		},
	}
	deadline := task.SentAt.Add(5 * time.Minute)
	clarification := Envelope{
		MessageID:      "msg_clarify",
		ThreadID:       thread.ThreadID,
		From:           "worker.smokevm",
		To:             []string{"collector.nullbot"},
		Type:           MessageTypeClarifyRequest,
		Payload:        []byte(`{"question":"which env?"}`),
		ReplyTo:        task.MessageID,
		Deadline:       &deadline,
		SentAt:         task.SentAt.Add(1 * time.Minute),
		IdempotencyKey: "idem_clarify",
		Trace:          Trace{CorrelationID: "corr_123"},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_clarify",
		},
	}
	response := Envelope{
		MessageID:      "msg_answer",
		ThreadID:       thread.ThreadID,
		From:           "collector.nullbot",
		To:             []string{"worker.smokevm"},
		Type:           MessageTypeClarifyResponse,
		Payload:        []byte(`{"answer":"prod-eu-1"}`),
		ReplyTo:        clarification.MessageID,
		SentAt:         task.SentAt.Add(2 * time.Minute),
		IdempotencyKey: "idem_answer",
		Trace:          Trace{CorrelationID: "corr_123"},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_answer",
		},
	}

	state, err := thread.PendingClarification(
		[]Envelope{task, clarification, response},
		task.SentAt.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatalf("PendingClarification() error = %v", err)
	}
	if state != nil {
		t.Fatalf("expected clarification to be resolved, got %#v", state)
	}
}
