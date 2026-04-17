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

	if document.EditionBoundary.RepoByTier["free"] != "hivebus" {
		t.Fatalf("expected free tier in hivebus, got %q", document.EditionBoundary.RepoByTier["free"])
	}

	if document.EditionBoundary.RepoByTier["pro"] != "hivebus-pro" {
		t.Fatalf("expected pro tier in hivebus-pro, got %q", document.EditionBoundary.RepoByTier["pro"])
	}

	if len(document.FeatureBoundary.CommunityFeatures) == 0 {
		t.Fatal("expected community feature boundary")
	}

	if len(document.ArtifactManifestFields) != 3 {
		t.Fatalf("expected artifact manifest fields, got %#v", document.ArtifactManifestFields)
	}

	if document.FeatureBoundary.Workledger.ProjectSelection == "" {
		t.Fatal("expected workledger project selection strategy")
	}

	if !document.WorkOrderGate["workledger_project_required"] {
		t.Fatal("expected workledger project gate")
	}

	if len(document.CapabilityLifecycle.MessageTypes) != 9 {
		t.Fatalf("expected 9 capability lifecycle message types, got %d", len(document.CapabilityLifecycle.MessageTypes))
	}

	if len(document.CapabilityLifecycle.RequiredFields) == 0 {
		t.Fatal("expected capability lifecycle required fields")
	}

	if len(document.CapabilityLifecycle.Consumers["sentinel"]) == 0 {
		t.Fatal("expected sentinel capability lifecycle consumer rules")
	}

	if len(document.CapabilityLifecycle.Consumers["workledger"]) == 0 {
		t.Fatal("expected workledger capability lifecycle consumer rules")
	}
}

func TestSampleCapabilityLifecycleValidatesAllEvents(t *testing.T) {
	t.Helper()

	sample := SampleCapabilityLifecycle()
	if len(sample.Messages) != 7 {
		t.Fatalf("expected 7 capability lifecycle messages, got %d", len(sample.Messages))
	}

	for _, envelope := range sample.Messages {
		if err := envelope.ValidateCapabilityLifecycle(); err != nil {
			t.Fatalf("ValidateCapabilityLifecycle(%s) error = %v", envelope.Type, err)
		}
	}
}
