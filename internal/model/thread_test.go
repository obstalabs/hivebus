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
				ArtifactID: "art_1",
				Kind:       "log",
				URI:        "s3://example/log.txt",
				SHA256:     "abc123",
			}}
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
