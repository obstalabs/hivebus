package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
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

func TestCapabilityDiscoveryPromotesTrustedCapabilitiesToPolling(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	baseTime := time.Date(2026, 4, 18, 8, 0, 0, 0, time.UTC)

	for i := 0; i < capabilityValidationSuccessThreshold; i++ {
		thread := sampleThread()
		thread.ThreadID = fmt.Sprintf("thr_capability_%d", i+1)
		thread.CreatedAt = baseTime.Add(time.Duration(i) * time.Hour)
		thread.UpdatedAt = thread.CreatedAt
		if _, err := st.AppendThread(t.Context(), thread); err != nil {
			t.Fatalf("AppendThread(%d) error = %v", i+1, err)
		}

		request := sampleWorkerTask(
			thread.ThreadID,
			fmt.Sprintf("msg_task_%d", i+1),
			fmt.Sprintf("idem_task_%d", i+1),
			"worker.smokevm",
		)
		request.SentAt = thread.CreatedAt.Add(time.Minute)
		if err := st.AppendEnvelope(t.Context(), request); err != nil {
			t.Fatalf("AppendEnvelope(task %d) error = %v", i+1, err)
		}

		accepted := sampleAcceptedEnvelope(
			request,
			"worker.smokevm",
			fmt.Sprintf("msg_accepted_%d", i+1),
			fmt.Sprintf("idem_accepted_%d", i+1),
			request.SentAt.Add(time.Minute),
		)
		lease, err := st.ClaimTask(
			t.Context(),
			"worker.smokevm",
			request.MessageID,
			accepted,
			5*time.Minute,
			request.SentAt.Add(time.Minute),
		)
		if err != nil {
			t.Fatalf("ClaimTask(%d) error = %v", i+1, err)
		}

		result := sampleResultEnvelopeWithTools(
			request,
			"worker.smokevm",
			fmt.Sprintf("msg_result_%d", i+1),
			fmt.Sprintf("idem_result_%d", i+1),
			request.SentAt.Add(2*time.Minute),
			"go-testing",
		)
		if _, err := st.CompleteLease(
			t.Context(),
			lease.LeaseID,
			"worker.smokevm",
			result,
			request.SentAt.Add(2*time.Minute),
		); err != nil {
			t.Fatalf("CompleteLease(%d) error = %v", i+1, err)
		}
	}

	records, err := st.ListDiscoveredCapabilities(t.Context(), "worker.smokevm", baseTime.Add(4*time.Hour))
	if err != nil {
		t.Fatalf("ListDiscoveredCapabilities() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 discovered capability, got %#v", records)
	}
	if records[0].TrustLevel != model.CapabilityTrustValidated || !records[0].PendingApproval {
		t.Fatalf("expected validated pending capability, got %#v", records[0])
	}

	capabilityThread := sampleThread()
	capabilityThread.ThreadID = "thr_capability_poll"
	capabilityThread.CreatedAt = baseTime.Add(5 * time.Hour)
	capabilityThread.UpdatedAt = capabilityThread.CreatedAt
	if _, err := st.AppendThread(t.Context(), capabilityThread); err != nil {
		t.Fatalf("AppendThread(capability poll) error = %v", err)
	}

	capabilityTask := sampleCapabilityTask(
		capabilityThread.ThreadID,
		"msg_capability_task",
		"idem_capability_task",
		"go-testing",
	)
	capabilityTask.SentAt = capabilityThread.CreatedAt.Add(time.Minute)
	if err := st.AppendEnvelope(t.Context(), capabilityTask); err != nil {
		t.Fatalf("AppendEnvelope(capability task) error = %v", err)
	}

	beforeApproval, err := st.PollTask(t.Context(), "worker.smokevm", nil, capabilityTask.SentAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("PollTask(before approval) error = %v", err)
	}
	if beforeApproval.Status != PollResultIdle {
		t.Fatalf("expected idle before approval, got %#v", beforeApproval)
	}

	approved, err := st.ApproveDiscoveredCapability(
		t.Context(),
		"worker.smokevm",
		"go-testing",
		"operator.root",
		baseTime.Add(6*time.Hour),
	)
	if err != nil {
		t.Fatalf("ApproveDiscoveredCapability() error = %v", err)
	}
	if approved.TrustLevel != model.CapabilityTrustTrusted || approved.PendingApproval {
		t.Fatalf("expected trusted approved capability, got %#v", approved)
	}

	afterApproval, err := st.PollTask(t.Context(), "worker.smokevm", nil, capabilityTask.SentAt.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("PollTask(after approval) error = %v", err)
	}
	if afterApproval.Status != PollResultAvailable || afterApproval.Task == nil ||
		afterApproval.Task.Envelope.MessageID != capabilityTask.MessageID {
		t.Fatalf("expected trusted capability task, got %#v", afterApproval)
	}
}

func TestCapabilityDiscoveryDecayRemovesTrustedCapabilityFromPolling(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	baseTime := time.Date(2026, 4, 18, 9, 0, 0, 0, time.UTC)

	thread := sampleThread()
	thread.ThreadID = "thr_capability_decay"
	thread.CreatedAt = baseTime
	thread.UpdatedAt = baseTime
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	request := sampleWorkerTask(thread.ThreadID, "msg_task_decay", "idem_task_decay", "worker.smokevm")
	request.SentAt = baseTime.Add(time.Minute)
	if err := st.AppendEnvelope(t.Context(), request); err != nil {
		t.Fatalf("AppendEnvelope(task) error = %v", err)
	}

	accepted := sampleAcceptedEnvelope(request, "worker.smokevm", "msg_accepted_decay", "idem_accepted_decay", request.SentAt.Add(time.Minute))
	lease, err := st.ClaimTask(
		t.Context(),
		"worker.smokevm",
		request.MessageID,
		accepted,
		5*time.Minute,
		request.SentAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("ClaimTask() error = %v", err)
	}

	result := sampleResultEnvelopeWithTools(
		request,
		"worker.smokevm",
		"msg_result_decay",
		"idem_result_decay",
		request.SentAt.Add(2*time.Minute),
		"go-testing",
	)
	if _, err := st.CompleteLease(
		t.Context(),
		lease.LeaseID,
		"worker.smokevm",
		result,
		request.SentAt.Add(2*time.Minute),
	); err != nil {
		t.Fatalf("CompleteLease() error = %v", err)
	}

	if _, err := st.ApproveDiscoveredCapability(
		t.Context(),
		"worker.smokevm",
		"go-testing",
		"operator.root",
		baseTime.Add(3*time.Minute),
	); err != nil {
		t.Fatalf("ApproveDiscoveredCapability() error = %v", err)
	}

	records, err := st.ListDiscoveredCapabilities(
		t.Context(),
		"worker.smokevm",
		baseTime.Add(capabilityDecayWindow).Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("ListDiscoveredCapabilities() error = %v", err)
	}
	if len(records) != 1 || !records[0].Decayed {
		t.Fatalf("expected decayed capability, got %#v", records)
	}

	capabilityThread := sampleThread()
	capabilityThread.ThreadID = "thr_capability_decay_poll"
	capabilityThread.CreatedAt = baseTime.Add(capabilityDecayWindow).Add(2 * time.Hour)
	capabilityThread.UpdatedAt = capabilityThread.CreatedAt
	if _, err := st.AppendThread(t.Context(), capabilityThread); err != nil {
		t.Fatalf("AppendThread(capability poll) error = %v", err)
	}

	capabilityTask := sampleCapabilityTask(
		capabilityThread.ThreadID,
		"msg_capability_decay_task",
		"idem_capability_decay_task",
		"go-testing",
	)
	capabilityTask.SentAt = capabilityThread.CreatedAt.Add(time.Minute)
	if err := st.AppendEnvelope(t.Context(), capabilityTask); err != nil {
		t.Fatalf("AppendEnvelope(capability task) error = %v", err)
	}

	poll, err := st.PollTask(
		t.Context(),
		"worker.smokevm",
		nil,
		capabilityTask.SentAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("PollTask() error = %v", err)
	}
	if poll.Status != PollResultIdle {
		t.Fatalf("expected idle after decay, got %#v", poll)
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

func sampleResultEnvelopeWithTools(
	request model.Envelope,
	workerID string,
	messageID string,
	idempotencyKey string,
	sentAt time.Time,
	toolsUsed ...string,
) model.Envelope {
	payload, err := json.Marshal(model.TaskResultFinalPayload{
		Status:    "done",
		ToolsUsed: toolsUsed,
	})
	if err != nil {
		panic(err)
	}

	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       request.ThreadID,
		From:           workerID,
		To:             []string{request.From},
		Type:           model.MessageTypeTaskResultFinal,
		Payload:        payload,
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
