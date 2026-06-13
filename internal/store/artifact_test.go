package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
)

func TestAppendArtifactPersistsEvidenceInThreadReplay(t *testing.T) {
	t.Helper()

	st, err := Open(filepath.Join(t.TempDir(), "hivebus.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	thread := model.Thread{
		ThreadID:     "thr_123",
		Title:        "Artifact replay test",
		Status:       model.ThreadStatusReported,
		CustomerTier: model.TierPro,
		Source:       "nullbot",
		Participants: []model.Participant{{
			ID:          "collector.nullbot",
			Type:        model.ParticipantTypeService,
			Kind:        model.ParticipantCollector,
			DisplayName: "Nullbot Collector",
			Visibility:  model.ParticipantVisibilityThread,
			Service: &model.ServiceParticipant{
				ServiceName: "nullbot",
			},
		}},
		CreatedAt: time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC),
	}
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	artifact := model.Artifact{
		ArtifactID:  "art_log",
		Name:        "smoke.log",
		Kind:        "log",
		URI:         "artifact://sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SizeBytes:   9,
		ContentType: "text/plain",
	}
	if err := st.AppendArtifact(t.Context(), thread.ThreadID, artifact, formatTime(thread.CreatedAt.Add(time.Minute))); err != nil {
		t.Fatalf("AppendArtifact() error = %v", err)
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.Thread.Evidence) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(snapshot.Thread.Evidence))
	}
	if snapshot.Thread.Evidence[0].ArtifactID != artifact.ArtifactID {
		t.Fatalf("expected artifact id %q, got %q", artifact.ArtifactID, snapshot.Thread.Evidence[0].ArtifactID)
	}
}
