package store

import (
	"errors"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
)

func TestClarificationBlocksCompletionUntilResponse(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	request := sampleWorkerTask(thread.ThreadID, "msg_task", "idem_task", "worker.smokevm")
	if err := st.AppendEnvelope(t.Context(), request); err != nil {
		t.Fatalf("AppendEnvelope(task) error = %v", err)
	}

	now := request.SentAt.Add(1 * time.Minute)
	accepted := sampleAcceptedEnvelope(request, "worker.smokevm", "msg_accepted", "idem_accepted", now)
	lease, err := st.ClaimTask(t.Context(), "worker.smokevm", request.MessageID, accepted, 5*time.Minute, now)
	if err != nil {
		t.Fatalf("ClaimTask() error = %v", err)
	}

	deadline := now.Add(5 * time.Minute)
	clarification := model.Envelope{
		MessageID:      "msg_clarify",
		ThreadID:       thread.ThreadID,
		From:           "worker.smokevm",
		To:             []string{request.From},
		Type:           model.MessageTypeClarifyRequest,
		Payload:        []byte(`{"question":"which release?"}`),
		ReplyTo:        request.MessageID,
		Deadline:       &deadline,
		SentAt:         now.Add(30 * time.Second),
		IdempotencyKey: "idem_clarify",
		Trace: model.Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_clarify",
		},
	}
	if err := st.AppendClarificationRequest(t.Context(), lease.LeaseID, "worker.smokevm", clarification, clarification.SentAt); err != nil {
		t.Fatalf("AppendClarificationRequest() error = %v", err)
	}

	result := sampleResultEnvelope(request, "worker.smokevm", "msg_result", "idem_result", now.Add(2*time.Minute))
	if _, err := st.CompleteLease(t.Context(), lease.LeaseID, "worker.smokevm", result, now.Add(2*time.Minute)); !errors.Is(err, ErrClarificationPending) {
		t.Fatalf("CompleteLease() error = %v, want %v", err, ErrClarificationPending)
	}

	response := model.Envelope{
		MessageID:      "msg_answer",
		ThreadID:       thread.ThreadID,
		From:           request.From,
		To:             []string{"worker.smokevm"},
		Type:           model.MessageTypeClarifyResponse,
		Payload:        []byte(`{"answer":"v0.17.2"}`),
		ReplyTo:        clarification.MessageID,
		SentAt:         now.Add(3 * time.Minute),
		IdempotencyKey: "idem_answer",
		Trace: model.Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_answer",
		},
	}
	if err := st.AppendClarificationResponse(t.Context(), thread.ThreadID, response, response.SentAt); err != nil {
		t.Fatalf("AppendClarificationResponse() error = %v", err)
	}

	completed, err := st.CompleteLease(t.Context(), lease.LeaseID, "worker.smokevm", result, now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("CompleteLease() after clarification error = %v", err)
	}
	if completed.Status != LeaseStatusCompleted {
		t.Fatalf("expected completed lease, got %q", completed.Status)
	}
}

func TestClarificationResponseRejectsExpiredAndStaleReplies(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	request := sampleWorkerTask(thread.ThreadID, "msg_task", "idem_task", "worker.smokevm")
	if err := st.AppendEnvelope(t.Context(), request); err != nil {
		t.Fatalf("AppendEnvelope(task) error = %v", err)
	}

	now := request.SentAt.Add(1 * time.Minute)
	accepted := sampleAcceptedEnvelope(request, "worker.smokevm", "msg_accepted", "idem_accepted", now)
	lease, err := st.ClaimTask(t.Context(), "worker.smokevm", request.MessageID, accepted, 5*time.Minute, now)
	if err != nil {
		t.Fatalf("ClaimTask() error = %v", err)
	}

	deadline := now.Add(1 * time.Minute)
	clarification := model.Envelope{
		MessageID:      "msg_clarify",
		ThreadID:       thread.ThreadID,
		From:           "worker.smokevm",
		To:             []string{request.From},
		Type:           model.MessageTypeClarifyRequest,
		Payload:        []byte(`{"question":"which release?"}`),
		ReplyTo:        request.MessageID,
		Deadline:       &deadline,
		SentAt:         now.Add(10 * time.Second),
		IdempotencyKey: "idem_clarify",
		Trace: model.Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_clarify",
		},
	}
	if err := st.AppendClarificationRequest(t.Context(), lease.LeaseID, "worker.smokevm", clarification, clarification.SentAt); err != nil {
		t.Fatalf("AppendClarificationRequest() error = %v", err)
	}

	expiredResponse := model.Envelope{
		MessageID:      "msg_answer_late",
		ThreadID:       thread.ThreadID,
		From:           request.From,
		To:             []string{"worker.smokevm"},
		Type:           model.MessageTypeClarifyResponse,
		Payload:        []byte(`{"answer":"late"}`),
		ReplyTo:        clarification.MessageID,
		SentAt:         deadline.Add(1 * time.Second),
		IdempotencyKey: "idem_answer_late",
		Trace: model.Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_answer_late",
		},
	}
	if err := st.AppendClarificationResponse(t.Context(), thread.ThreadID, expiredResponse, expiredResponse.SentAt); !errors.Is(err, ErrClarificationExpired) {
		t.Fatalf("AppendClarificationResponse(expired) error = %v, want %v", err, ErrClarificationExpired)
	}

	timelyResponse := expiredResponse
	timelyResponse.MessageID = "msg_answer"
	timelyResponse.IdempotencyKey = "idem_answer"
	timelyResponse.SentAt = deadline.Add(-10 * time.Second)
	if err := st.AppendClarificationResponse(t.Context(), thread.ThreadID, timelyResponse, timelyResponse.SentAt); err != nil {
		t.Fatalf("AppendClarificationResponse() error = %v", err)
	}

	staleResponse := timelyResponse
	staleResponse.MessageID = "msg_answer_again"
	staleResponse.IdempotencyKey = "idem_answer_again"
	staleResponse.SentAt = deadline.Add(-5 * time.Second)
	if err := st.AppendClarificationResponse(t.Context(), thread.ThreadID, staleResponse, staleResponse.SentAt); !errors.Is(err, ErrClarificationStale) {
		t.Fatalf("AppendClarificationResponse(stale) error = %v, want %v", err, ErrClarificationStale)
	}
}
