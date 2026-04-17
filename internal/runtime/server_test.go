package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/artifact"
	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/store"
)

func TestThreadLifecycleOverHTTP(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), nil)

	thread := sampleThread()
	threadBody, err := json.Marshal(thread)
	if err != nil {
		t.Fatalf("Marshal(thread) error = %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/v0/threads", bytes.NewReader(threadBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST /v0/threads status = %d, body = %s", createRec.Code, createRec.Body.String())
	}

	envelope := sampleEnvelope(thread.ThreadID, "msg_1", "idem_1")
	envelopeBody, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("Marshal(envelope) error = %v", err)
	}

	appendReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/threads/"+thread.ThreadID+"/messages",
		bytes.NewReader(envelopeBody),
	)
	appendReq.Header.Set("Content-Type", "application/json")
	appendRec := httptest.NewRecorder()
	handler.ServeHTTP(appendRec, appendReq)
	if appendRec.Code != http.StatusAccepted {
		t.Fatalf("POST /v0/threads/{id}/messages status = %d, body = %s", appendRec.Code, appendRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v0/threads/"+thread.ThreadID, nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /v0/threads/{id} status = %d, body = %s", getRec.Code, getRec.Body.String())
	}

	var snapshot store.ThreadSnapshot
	if err := json.Unmarshal(getRec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("Unmarshal(snapshot) error = %v", err)
	}

	if snapshot.Thread.ThreadID != thread.ThreadID {
		t.Fatalf("expected thread_id %q, got %q", thread.ThreadID, snapshot.Thread.ThreadID)
	}
	if len(snapshot.Envelopes) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(snapshot.Envelopes))
	}
	if snapshot.Envelopes[0].MessageID != envelope.MessageID {
		t.Fatalf("expected message_id %q, got %q", envelope.MessageID, snapshot.Envelopes[0].MessageID)
	}
}

func TestAppendEnvelopeRejectsThreadPathMismatch(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), nil)

	thread := sampleThread()
	mustSeedThread(t, st, thread)

	envelope := sampleEnvelope("thr_other", "msg_1", "idem_1")
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("Marshal(envelope) error = %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/v0/threads/"+thread.ThreadID+"/messages",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHealthzReportsStoreAvailability(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "hivebus.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	return st
}

func openTestArtifactStore(t *testing.T) *artifact.Store {
	t.Helper()

	st, err := artifact.Open(filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatalf("artifact.Open() error = %v", err)
	}

	return st
}

func mustSeedThread(t *testing.T, st *store.Store, thread model.Thread) {
	t.Helper()

	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}
}

func sampleThread() model.Thread {
	now := time.Date(2026, 4, 15, 7, 0, 0, 0, time.UTC)

	return model.Thread{
		ThreadID:     "thr_123",
		Title:        "Clawbot smoke lane is unstable",
		Status:       model.ThreadStatusReported,
		CustomerTier: model.TierPro,
		Source:       "nullbot",
		Participants: []model.Participant{
			{ID: "collector.nullbot", Kind: model.ParticipantCollector},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func sampleEnvelope(threadID string, messageID string, idempotencyKey string) model.Envelope {
	now := time.Date(2026, 4, 15, 7, 1, 0, 0, time.UTC)

	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       threadID,
		From:           "collector.nullbot",
		To:             []string{"agent.runtime"},
		Type:           model.MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"smoke lane unstable"}`),
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
