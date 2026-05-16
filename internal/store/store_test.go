package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/spec"
)

func TestStoreAppendAndReplayThread(t *testing.T) {
	t.Helper()

	st := openTestStore(t)

	thread := sampleThread()
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	first := sampleEnvelope(thread.ThreadID, "msg_1", "idem_1")
	if err := st.AppendEnvelope(t.Context(), first); err != nil {
		t.Fatalf("AppendEnvelope(first) error = %v", err)
	}

	second := sampleEnvelope(thread.ThreadID, "msg_2", "idem_2")
	second.SentAt = second.SentAt.Add(1 * time.Minute)
	if err := st.AppendEnvelope(t.Context(), second); err != nil {
		t.Fatalf("AppendEnvelope(second) error = %v", err)
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}

	if snapshot.Thread.ThreadID != thread.ThreadID {
		t.Fatalf("expected thread_id %q, got %q", thread.ThreadID, snapshot.Thread.ThreadID)
	}
	if len(snapshot.Envelopes) != 2 {
		t.Fatalf("expected 2 envelopes, got %d", len(snapshot.Envelopes))
	}
	if snapshot.Envelopes[0].MessageID != first.MessageID {
		t.Fatalf("expected first message_id %q, got %q", first.MessageID, snapshot.Envelopes[0].MessageID)
	}
	if snapshot.Envelopes[1].MessageID != second.MessageID {
		t.Fatalf("expected second message_id %q, got %q", second.MessageID, snapshot.Envelopes[1].MessageID)
	}
}

func TestStoreRejectsDuplicateThread(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()

	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	if _, err := st.AppendThread(t.Context(), thread); !errors.Is(err, ErrDuplicateThread) {
		t.Fatalf("AppendThread() error = %v, want %v", err, ErrDuplicateThread)
	}
}

func TestStoreRejectsEnvelopeForMissingThread(t *testing.T) {
	t.Helper()

	st := openTestStore(t)

	err := st.AppendEnvelope(t.Context(), sampleEnvelope("thr_missing", "msg_1", "idem_1"))
	if !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("AppendEnvelope() error = %v, want %v", err, ErrThreadNotFound)
	}
}

func TestStoreRejectsDuplicateMessageAndIdempotencyKey(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()

	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	envelope := sampleEnvelope(thread.ThreadID, "msg_1", "idem_1")
	if err := st.AppendEnvelope(t.Context(), envelope); err != nil {
		t.Fatalf("AppendEnvelope() error = %v", err)
	}

	if err := st.AppendEnvelope(t.Context(), envelope); !errors.Is(err, ErrDuplicateMessage) {
		t.Fatalf("AppendEnvelope(duplicate message) error = %v, want %v", err, ErrDuplicateMessage)
	}

	sameIdempotency := sampleEnvelope(thread.ThreadID, "msg_2", "idem_1")
	if err := st.AppendEnvelope(t.Context(), sameIdempotency); !errors.Is(err, ErrDuplicateIdempotencyKey) {
		t.Fatalf("AppendEnvelope(duplicate idempotency) error = %v, want %v", err, ErrDuplicateIdempotencyKey)
	}
}

func TestStoreAppendsNeuroRouterRunReceipts(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	sample := spec.SampleNeuroRouterRunLifecycle()
	if _, err := st.AppendThread(t.Context(), sample.Thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	for _, envelope := range sample.Messages {
		if err := st.AppendEnvelope(t.Context(), envelope); err != nil {
			t.Fatalf("AppendEnvelope(%s) error = %v", envelope.Type, err)
		}
	}

	snapshot, err := st.LoadThread(t.Context(), sample.Thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}

	if len(snapshot.Envelopes) != len(sample.Messages) {
		t.Fatalf("expected %d envelopes, got %d", len(sample.Messages), len(snapshot.Envelopes))
	}
	if snapshot.Envelopes[0].Type != model.MessageTypeNRRunStarted {
		t.Fatalf("expected first envelope %s, got %s", model.MessageTypeNRRunStarted, snapshot.Envelopes[0].Type)
	}
	if snapshot.Envelopes[len(snapshot.Envelopes)-1].Type != model.MessageTypeNRRunAuditAnchor {
		t.Fatalf("expected final envelope %s, got %s", model.MessageTypeNRRunAuditAnchor, snapshot.Envelopes[len(snapshot.Envelopes)-1].Type)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "events.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	return st
}

func sampleThread() model.Thread {
	now := time.Date(2026, 4, 15, 6, 0, 0, 0, time.UTC)

	return model.Thread{
		ThreadID:     "thr_123",
		Title:        "Codex lost the smoke VM handle",
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
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func sampleEnvelope(threadID string, messageID string, idempotencyKey string) model.Envelope {
	now := time.Date(2026, 4, 15, 6, 1, 0, 0, time.UTC)

	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       threadID,
		From:           "collector.nullbot",
		To:             []string{"agent.investigator"},
		Type:           model.MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"smoke vm context lost"}`),
		SentAt:         now,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID: "corr_123",
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_123",
		},
	}
}
