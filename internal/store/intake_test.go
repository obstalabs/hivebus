package store

import (
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

func TestCreateIntakePersistsThreadArtifactsAndInitialTask(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	now := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)

	thread := model.Thread{
		ThreadID:     "thr_intake_001",
		Title:        "Nullbot opened a smoke lane issue",
		Status:       model.ThreadStatusReported,
		CustomerTier: model.TierPro,
		Source:       "nullbot",
		Participants: []model.Participant{
			{
				ID:          "collector.nullbot",
				Type:        model.ParticipantTypeService,
				Kind:        model.ParticipantCollector,
				DisplayName: "Nullbot Collector",
				Visibility:  model.ParticipantVisibilityThread,
				Service: &model.ServiceParticipant{
					ServiceName: "nullbot",
				},
			},
			{
				ID:          "agent.field.nullbot",
				Type:        model.ParticipantTypeAgent,
				Kind:        model.ParticipantAgent,
				DisplayName: "Field Nullbot",
				Visibility:  model.ParticipantVisibilityThread,
				Agent: &model.AgentParticipant{
					AgentID: "nullbot-edge",
				},
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	artifact := model.Artifact{
		ArtifactID:  "art_smoke_log",
		Name:        "smoke.log",
		Kind:        "log",
		URI:         "artifact://sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SizeBytes:   9,
		ContentType: "text/plain",
	}

	task := sampleEnvelope(thread.ThreadID, "msg_intake_001", "idem_intake_001")
	task.From = "collector.nullbot"
	task.To = []string{"agent.field.nullbot"}
	task.ArtifactIDs = []string{artifact.ArtifactID}
	task.SentAt = now.Add(1 * time.Minute)

	snapshot, err := st.CreateIntake(t.Context(), thread, []model.Artifact{artifact}, task)
	if err != nil {
		t.Fatalf("CreateIntake() error = %v", err)
	}

	if len(snapshot.Thread.Evidence) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(snapshot.Thread.Evidence))
	}
	if len(snapshot.Envelopes) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(snapshot.Envelopes))
	}
	if snapshot.Envelopes[0].MessageID != task.MessageID {
		t.Fatalf("expected task message %q, got %q", task.MessageID, snapshot.Envelopes[0].MessageID)
	}
}

func TestTransitionThreadReplaysLatestStatus(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	thread.Status = model.ThreadStatusInvestigating
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	readyAt := thread.UpdatedAt.Add(5 * time.Minute)
	snapshot, err := st.TransitionThread(t.Context(), thread.ThreadID, model.ThreadStatusReadyForWork, readyAt)
	if err != nil {
		t.Fatalf("TransitionThread() error = %v", err)
	}

	if snapshot.Thread.Status != model.ThreadStatusReadyForWork {
		t.Fatalf("expected status %q, got %q", model.ThreadStatusReadyForWork, snapshot.Thread.Status)
	}
	if !snapshot.Thread.UpdatedAt.Equal(readyAt) {
		t.Fatalf("expected updated_at %v, got %v", readyAt, snapshot.Thread.UpdatedAt)
	}
}
