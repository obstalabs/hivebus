package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
)

func TestListThreadEventsReplaysInSequenceOrder(t *testing.T) {
	t.Helper()

	st, err := Open(filepath.Join(t.TempDir(), "hivebus.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	thread := sampleThread()
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	request := sampleEnvelope(thread.ThreadID, "msg_task", "idem_task")
	if err := st.AppendEnvelope(t.Context(), request); err != nil {
		t.Fatalf("AppendEnvelope(task) error = %v", err)
	}

	accepted := model.Envelope{
		MessageID:      "msg_accept",
		ThreadID:       thread.ThreadID,
		From:           "worker.smokevm",
		To:             []string{request.From},
		Type:           model.MessageTypeTaskAccepted,
		Payload:        []byte(`{"status":"accepted"}`),
		ReplyTo:        request.MessageID,
		SentAt:         request.SentAt.Add(1 * time.Minute),
		IdempotencyKey: "idem_accept",
		Trace: model.Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_accept",
		},
	}
	lease, err := st.ClaimTask(t.Context(), "worker.smokevm", request.MessageID, accepted, 5*time.Minute, request.SentAt.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("ClaimTask() error = %v", err)
	}

	partial := model.Envelope{
		MessageID:      "msg_partial",
		ThreadID:       thread.ThreadID,
		From:           "worker.smokevm",
		To:             []string{request.From},
		Type:           model.MessageTypeTaskResultPart,
		Payload:        []byte(`{"chunk":"running smoke"}`),
		ReplyTo:        request.MessageID,
		SentAt:         request.SentAt.Add(2 * time.Minute),
		IdempotencyKey: "idem_partial",
		Trace: model.Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_partial",
		},
	}
	if err := st.AppendLeaseResultPart(t.Context(), lease.LeaseID, "worker.smokevm", partial, partial.SentAt); err != nil {
		t.Fatalf("AppendLeaseResultPart() error = %v", err)
	}

	events, err := st.ListThreadEvents(t.Context(), thread.ThreadID, 0, 16)
	if err != nil {
		t.Fatalf("ListThreadEvents() error = %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
	for i := 1; i < len(events); i++ {
		if events[i].Sequence <= events[i-1].Sequence {
			t.Fatalf("expected increasing sequences, got %#v then %#v", events[i-1], events[i])
		}
	}
	if events[4].Envelope == nil || events[4].Envelope.Type != model.MessageTypeTaskResultPart {
		t.Fatalf("expected partial result event, got %#v", events[4])
	}
}

func TestListThreadEventsResumesAfterLastSequence(t *testing.T) {
	t.Helper()

	st, err := Open(filepath.Join(t.TempDir(), "hivebus.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	thread := sampleThread()
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	first := sampleEnvelope(thread.ThreadID, "msg_1", "idem_1")
	second := sampleEnvelope(thread.ThreadID, "msg_2", "idem_2")
	second.SentAt = first.SentAt.Add(1 * time.Minute)
	if err := st.AppendEnvelope(t.Context(), first); err != nil {
		t.Fatalf("AppendEnvelope(first) error = %v", err)
	}
	if err := st.AppendEnvelope(t.Context(), second); err != nil {
		t.Fatalf("AppendEnvelope(second) error = %v", err)
	}

	events, err := st.ListThreadEvents(t.Context(), thread.ThreadID, 2, 16)
	if err != nil {
		t.Fatalf("ListThreadEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 resumed event, got %d", len(events))
	}
	if events[0].Envelope == nil || events[0].Envelope.MessageID != second.MessageID {
		t.Fatalf("expected second resumed envelope, got %#v", events[0])
	}
}
