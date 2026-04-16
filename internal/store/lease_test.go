package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

func TestPollClaimRenewCompleteLifecycle(t *testing.T) {
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
	poll, err := st.PollTask(t.Context(), "worker.smokevm", []string{}, now)
	if err != nil {
		t.Fatalf("PollTask() error = %v", err)
	}
	if poll.Status != PollResultAvailable {
		t.Fatalf("expected available, got %q", poll.Status)
	}
	if poll.Task == nil || poll.Task.Envelope.MessageID != request.MessageID {
		t.Fatalf("unexpected poll task %#v", poll.Task)
	}

	accepted := sampleAcceptedEnvelope(request, "worker.smokevm", "msg_accepted", "idem_accepted", now)
	lease, err := st.ClaimTask(
		t.Context(),
		"worker.smokevm",
		request.MessageID,
		accepted,
		2*time.Minute,
		now,
	)
	if err != nil {
		t.Fatalf("ClaimTask() error = %v", err)
	}

	busyPoll, err := st.PollTask(t.Context(), "worker.smokevm", nil, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("PollTask(busy) error = %v", err)
	}
	if busyPoll.Status != PollResultBusy {
		t.Fatalf("expected busy, got %q", busyPoll.Status)
	}

	renewed, err := st.RenewLease(t.Context(), lease.LeaseID, "worker.smokevm", 3*time.Minute, now.Add(1*time.Minute))
	if err != nil {
		t.Fatalf("RenewLease() error = %v", err)
	}
	if !renewed.LeasedUntil.After(lease.LeasedUntil) {
		t.Fatalf("expected renewed lease_until after %v, got %v", lease.LeasedUntil, renewed.LeasedUntil)
	}

	result := sampleResultEnvelope(request, "worker.smokevm", "msg_result", "idem_result", now.Add(2*time.Minute))
	completed, err := st.CompleteLease(t.Context(), lease.LeaseID, "worker.smokevm", result, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("CompleteLease() error = %v", err)
	}
	if completed.Status != LeaseStatusCompleted {
		t.Fatalf("expected completed lease, got %q", completed.Status)
	}

	idlePoll, err := st.PollTask(t.Context(), "worker.smokevm", nil, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("PollTask(idle) error = %v", err)
	}
	if idlePoll.Status != PollResultIdle {
		t.Fatalf("expected idle, got %q", idlePoll.Status)
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.LeaseReceipts) != 3 {
		t.Fatalf("expected 3 lease receipts, got %d", len(snapshot.LeaseReceipts))
	}
}

func TestLeaseExpiryReturnsTaskToQueueDeterministically(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	request := sampleCapabilityTask(thread.ThreadID, "msg_task_cap", "idem_task_cap", "incident.diagnose")
	if err := st.AppendEnvelope(t.Context(), request); err != nil {
		t.Fatalf("AppendEnvelope(task) error = %v", err)
	}

	now := request.SentAt.Add(1 * time.Minute)
	accepted := sampleAcceptedEnvelope(request, "worker.one", "msg_accepted_1", "idem_accepted_1", now)
	lease, err := st.ClaimTask(t.Context(), "worker.one", request.MessageID, accepted, 1*time.Minute, now)
	if err != nil {
		t.Fatalf("ClaimTask() error = %v", err)
	}

	poll, err := st.PollTask(
		t.Context(),
		"worker.two",
		[]string{"incident.diagnose"},
		lease.LeasedUntil.Add(1*time.Second),
	)
	if err != nil {
		t.Fatalf("PollTask() error = %v", err)
	}
	if poll.Status != PollResultAvailable {
		t.Fatalf("expected available after expiry, got %q", poll.Status)
	}
	if poll.Task == nil || poll.Task.Envelope.MessageID != request.MessageID {
		t.Fatalf("unexpected poll task %#v", poll.Task)
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.LeaseReceipts) != 2 {
		t.Fatalf("expected 2 lease receipts, got %d", len(snapshot.LeaseReceipts))
	}
	if snapshot.LeaseReceipts[1].Action != LeaseReceiptExpired {
		t.Fatalf("expected expiry receipt, got %q", snapshot.LeaseReceipts[1].Action)
	}
}

func TestRenewExpiredLeaseAndDuplicateCompletionAreRejected(t *testing.T) {
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
	lease, err := st.ClaimTask(t.Context(), "worker.smokevm", request.MessageID, accepted, 1*time.Minute, now)
	if err != nil {
		t.Fatalf("ClaimTask() error = %v", err)
	}

	if _, err := st.RenewLease(
		t.Context(),
		lease.LeaseID,
		"worker.smokevm",
		1*time.Minute,
		lease.LeasedUntil.Add(1*time.Second),
	); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("RenewLease() error = %v, want %v", err, ErrLeaseExpired)
	}

	reclaimedAccepted := sampleAcceptedEnvelope(
		request,
		"worker.other",
		"msg_accepted_reclaim",
		"idem_accepted_reclaim",
		lease.LeasedUntil.Add(2*time.Second),
	)
	reclaimedLease, err := st.ClaimTask(
		t.Context(),
		"worker.other",
		request.MessageID,
		reclaimedAccepted,
		1*time.Minute,
		lease.LeasedUntil.Add(2*time.Second),
	)
	if err != nil {
		t.Fatalf("ClaimTask(reclaim) error = %v", err)
	}

	result := sampleResultEnvelope(
		request,
		"worker.other",
		"msg_result",
		"idem_result",
		reclaimedLease.ClaimedAt.Add(10*time.Second),
	)
	if _, err := st.CompleteLease(
		t.Context(),
		reclaimedLease.LeaseID,
		"worker.other",
		result,
		reclaimedLease.ClaimedAt.Add(10*time.Second),
	); err != nil {
		t.Fatalf("CompleteLease() error = %v", err)
	}

	if _, err := st.CompleteLease(
		t.Context(),
		reclaimedLease.LeaseID,
		"worker.other",
		result,
		reclaimedLease.ClaimedAt.Add(20*time.Second),
	); !errors.Is(err, ErrLeaseFinalized) {
		t.Fatalf("CompleteLease(duplicate) error = %v, want %v", err, ErrLeaseFinalized)
	}
}

func sampleAcceptedEnvelope(
	request model.Envelope,
	workerID string,
	messageID string,
	idempotencyKey string,
	sentAt time.Time,
) model.Envelope {
	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       request.ThreadID,
		From:           workerID,
		To:             []string{request.From},
		Type:           model.MessageTypeTaskAccepted,
		Payload:        json.RawMessage(`{"status":"accepted"}`),
		ReplyTo:        request.MessageID,
		SentAt:         sentAt,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + messageID,
		},
	}
}

func sampleResultEnvelope(
	request model.Envelope,
	workerID string,
	messageID string,
	idempotencyKey string,
	sentAt time.Time,
) model.Envelope {
	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       request.ThreadID,
		From:           workerID,
		To:             []string{request.From},
		Type:           model.MessageTypeTaskResultFinal,
		Payload:        json.RawMessage(`{"status":"done"}`),
		ReplyTo:        request.MessageID,
		SentAt:         sentAt,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + messageID,
		},
	}
}

func sampleCapabilityTask(
	threadID string,
	messageID string,
	idempotencyKey string,
	capability string,
) model.Envelope {
	now := time.Date(2026, 4, 15, 6, 1, 0, 0, time.UTC)

	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       threadID,
		From:           "collector.nullbot",
		Capability:     capability,
		Type:           model.MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"smoke vm context lost"}`),
		SentAt:         now,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID: "corr_" + messageID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + messageID,
		},
	}
}

func sampleWorkerTask(
	threadID string,
	messageID string,
	idempotencyKey string,
	workerID string,
) model.Envelope {
	now := time.Date(2026, 4, 15, 6, 1, 0, 0, time.UTC)

	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       threadID,
		From:           "collector.nullbot",
		To:             []string{workerID},
		Type:           model.MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"smoke vm context lost"}`),
		SentAt:         now,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID: "corr_" + messageID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + messageID,
		},
	}
}
