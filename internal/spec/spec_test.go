package spec

import "testing"

func TestSampleCaseBuildsWorkOrderForSameThread(t *testing.T) {
	t.Helper()

	sample := SampleCase()

	if sample.WorkOrder.SourceThreadID != sample.Thread.ThreadID {
		t.Fatalf("expected work order source thread %q, got %q", sample.Thread.ThreadID, sample.WorkOrder.SourceThreadID)
	}

	if len(sample.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(sample.Messages))
	}
}

func TestV0DeclaresWorkledgerAsTrackingSystem(t *testing.T) {
	t.Helper()

	document := V0()

	if document.TrackingSystem != "workledger" {
		t.Fatalf("expected tracking system workledger, got %q", document.TrackingSystem)
	}

	if document.OptionalBridge != "hiveram.com" {
		t.Fatalf("expected optional bridge hiveram.com, got %q", document.OptionalBridge)
	}
}
