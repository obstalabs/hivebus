package work

import (
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

func TestDraftFromThreadBuildsVerifiedWorkOrder(t *testing.T) {
	t.Helper()

	thread := model.Thread{
		ThreadID:     "thr_123",
		Title:        "X is broken",
		Status:       model.ThreadStatusReadyForWork,
		CustomerTier: model.TierTeams,
		Source:       "nullbot",
		Participants: []model.Participant{
			testCollectorParticipant(),
			testAgentParticipant("agent.investigator"),
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC),
	}

	diagnosis := model.Diagnosis{
		Problem:             "Ingress requests fail with 502 errors during peak load.",
		LikelyCause:         "The upstream pool is exhausting available Postgres connections.",
		ProposedRemediation: []string{"Raise the Postgres connection ceiling.", "Reduce idle worker fan-out in the API deployment."},
		EvidenceIDs:         []string{"art_logs", "art_pg_stat"},
		Confidence:          model.ConfidenceHigh,
		Verified:            true,
	}

	draft, err := DraftFromThread("hivebus", thread, diagnosis)
	if err != nil {
		t.Fatalf("DraftFromThread() error = %v", err)
	}

	if draft.Priority != "P1" {
		t.Fatalf("expected priority P1, got %q", draft.Priority)
	}

	if draft.TrackingSystem != "workledger" {
		t.Fatalf("expected tracking system workledger, got %q", draft.TrackingSystem)
	}

	if draft.WorkledgerProject != "hivebus" {
		t.Fatalf("expected workledger project hivebus, got %q", draft.WorkledgerProject)
	}
}

func TestDraftFromThreadRejectsUnverifiedDiagnosis(t *testing.T) {
	t.Helper()

	thread := model.Thread{
		ThreadID:     "thr_123",
		Title:        "X is broken",
		Status:       model.ThreadStatusReadyForWork,
		CustomerTier: model.TierPro,
		Source:       "nullbot",
		Participants: []model.Participant{
			testCollectorParticipant(),
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC),
	}

	diagnosis := model.Diagnosis{
		Problem:             "Ingress requests fail with 502 errors during peak load.",
		LikelyCause:         "The upstream pool is exhausting available Postgres connections.",
		ProposedRemediation: []string{"Raise the Postgres connection ceiling."},
		EvidenceIDs:         []string{"art_logs"},
		Confidence:          model.ConfidenceHigh,
	}

	if _, err := DraftFromThread("hivebus", thread, diagnosis); err == nil {
		t.Fatal("DraftFromThread() expected an error")
	}
}

func TestDraftFromThreadRejectsMissingInfo(t *testing.T) {
	t.Helper()

	thread := model.Thread{
		ThreadID:     "thr_123",
		Title:        "X is broken",
		Status:       model.ThreadStatusReadyForWork,
		CustomerTier: model.TierFree,
		Source:       "nullbot",
		Participants: []model.Participant{
			testCollectorParticipant(),
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC),
	}

	diagnosis := model.Diagnosis{
		Problem:             "Ingress requests fail with 502 errors during peak load.",
		LikelyCause:         "The upstream pool is exhausting available Postgres connections.",
		ProposedRemediation: []string{"Raise the Postgres connection ceiling."},
		MissingInfo:         []string{"Need the exact deployment diff."},
		EvidenceIDs:         []string{"art_logs"},
		Confidence:          model.ConfidenceHigh,
		Verified:            true,
	}

	if _, err := DraftFromThread("hivebus", thread, diagnosis); err == nil {
		t.Fatal("DraftFromThread() expected an error")
	}
}

func TestDraftFromThreadRejectsThreadThatIsNotReady(t *testing.T) {
	t.Helper()

	thread := model.Thread{
		ThreadID:     "thr_123",
		Title:        "X is broken",
		Status:       model.ThreadStatusInvestigating,
		CustomerTier: model.TierFree,
		Source:       "nullbot",
		Participants: []model.Participant{
			testCollectorParticipant(),
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC),
	}

	diagnosis := model.Diagnosis{
		Problem:             "Ingress requests fail with 502 errors during peak load.",
		LikelyCause:         "The upstream pool is exhausting available Postgres connections.",
		ProposedRemediation: []string{"Raise the Postgres connection ceiling."},
		EvidenceIDs:         []string{"art_logs"},
		Confidence:          model.ConfidenceHigh,
		Verified:            true,
	}

	if _, err := DraftFromThread("hivebus", thread, diagnosis); err == nil {
		t.Fatal("DraftFromThread() expected an error")
	}
}

func TestDraftFromThreadAssignsP3ForFreeTier(t *testing.T) {
	t.Helper()

	thread := model.Thread{
		ThreadID:     "thr_123",
		Title:        "X is broken",
		Status:       model.ThreadStatusReadyForWork,
		CustomerTier: model.TierFree,
		Source:       "nullbot",
		Participants: []model.Participant{
			testCollectorParticipant(),
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC),
	}

	diagnosis := model.Diagnosis{
		Problem:             "Ingress requests fail with 502 errors during peak load.",
		LikelyCause:         "The upstream pool is exhausting available Postgres connections.",
		ProposedRemediation: []string{"Raise the Postgres connection ceiling."},
		EvidenceIDs:         []string{"art_logs"},
		Confidence:          model.ConfidenceHigh,
		Verified:            true,
	}

	draft, err := DraftFromThread("hivebus", thread, diagnosis)
	if err != nil {
		t.Fatalf("DraftFromThread() error = %v", err)
	}

	if draft.Priority != "P3" {
		t.Fatalf("expected priority P3, got %q", draft.Priority)
	}
}

func TestDraftFromThreadRejectsMissingWorkledgerProject(t *testing.T) {
	t.Helper()

	thread := model.Thread{
		ThreadID:     "thr_123",
		Title:        "X is broken",
		Status:       model.ThreadStatusReadyForWork,
		CustomerTier: model.TierPro,
		Source:       "nullbot",
		Participants: []model.Participant{
			testCollectorParticipant(),
		},
		CreatedAt: time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC),
	}

	diagnosis := model.Diagnosis{
		Problem:             "Ingress requests fail with 502 errors during peak load.",
		LikelyCause:         "The upstream pool is exhausting available Postgres connections.",
		ProposedRemediation: []string{"Raise the Postgres connection ceiling."},
		EvidenceIDs:         []string{"art_logs"},
		Confidence:          model.ConfidenceHigh,
		Verified:            true,
	}

	if _, err := DraftFromThread("", thread, diagnosis); err == nil {
		t.Fatal("DraftFromThread() expected an error")
	}
}

func testCollectorParticipant() model.Participant {
	return model.Participant{
		ID:          "collector.nullbot",
		Type:        model.ParticipantTypeService,
		Kind:        model.ParticipantCollector,
		DisplayName: "Nullbot Collector",
		Visibility:  model.ParticipantVisibilityThread,
		Service: &model.ServiceParticipant{
			ServiceName: "nullbot",
		},
	}
}

func testAgentParticipant(id string) model.Participant {
	return model.Participant{
		ID:          id,
		Type:        model.ParticipantTypeAgent,
		Kind:        model.ParticipantAgent,
		DisplayName: "Investigator Agent",
		Visibility:  model.ParticipantVisibilityThread,
		Agent: &model.AgentParticipant{
			AgentID: id,
		},
	}
}
