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

func TestStoreRejectsDirectVerifiedPromotionTruthAppend(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	tests := []struct {
		name     string
		status   model.PromotionStatus
		typeName model.MessageType
	}{
		{
			name:     "empty status diagnosis",
			status:   "",
			typeName: model.MessageTypeDiagnosisPropose,
		},
		{
			name:     "passed status diagnosis",
			status:   model.PromotionStatusPassed,
			typeName: model.MessageTypeDiagnosisPropose,
		},
		{
			name:     "passed status work order",
			status:   model.PromotionStatusPassed,
			typeName: model.MessageTypeWorkOrderCreate,
		},
	}

	for i, tt := range tests {
		envelope := sampleEnvelope(thread.ThreadID, "msg_promotion_"+tt.name, "idem_promotion_"+tt.name)
		envelope.Type = tt.typeName
		envelope.Trace.Verified = true
		envelope.Trace.PromotionStatus = tt.status
		if tt.typeName == model.MessageTypeDiagnosisPropose {
			envelope.Payload = json.RawMessage(`{
				"problem":"promotion append bypass",
				"likely_cause":"store append should reject authoritative promotion truth",
				"proposed_remediation":["use /promote instead of AppendEnvelope"],
				"evidence_ids":["art_smoke_log"],
				"confidence":"high",
				"verified":true
			}`)
		} else {
			envelope.Payload = json.RawMessage(`{
				"tracking_system":"workledger",
				"workledger_project":"hivebus",
				"work_order_id":64,
				"work_order_title":"Prevent append-path promotion truth",
				"source_thread_id":"` + thread.ThreadID + `",
				"optional_sync_targets":["hiveram.com"],
				"confidence":"high",
				"evidence_ids":["art_smoke_log"]
			}`)
		}
		envelope.MessageID = envelope.MessageID + string(rune('a'+i))
		envelope.IdempotencyKey = envelope.IdempotencyKey + string(rune('a'+i))

		if err := st.AppendEnvelope(t.Context(), envelope); err == nil {
			t.Fatalf("%s: expected AppendEnvelope() rejection", tt.name)
		}
	}
}

func TestStoreTracksPendingPromotionLaneSeparatelyFromVerifiedEnvelopes(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	pendingEnvelope := sampleEnvelope(thread.ThreadID, "msg_diag", "idem_diag")
	pendingEnvelope.Type = model.MessageTypeDiagnosisPropose
	pendingEnvelope.Trace.Verified = true
	pendingEnvelope.Trace.PromotionStatus = model.PromotionStatusPending
	record := PromotionPendingRecord{
		PendingMessageID: "pending_msg_diag",
		Envelope:         pendingEnvelope,
		Status:           model.PromotionStatusPending,
		UpdatedAt:        pendingEnvelope.SentAt,
	}
	if err := st.RecordPromotionPending(t.Context(), record); err != nil {
		t.Fatalf("RecordPromotionPending() error = %v", err)
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.Envelopes) != 0 {
		t.Fatalf("expected pending promotion to stay out of verified envelopes, got %#v", snapshot.Envelopes)
	}
	if len(snapshot.PendingPromotions) != 1 {
		t.Fatalf("expected one pending promotion record, got %#v", snapshot.PendingPromotions)
	}

	verifiedDiagnosis := promotedDiagnosisEnvelope(
		thread.ThreadID,
		"msg_diag_verified",
		"idem_diag_verified",
		pendingEnvelope.SentAt.Add(time.Minute),
	)
	verifiedWorkOrder := promotedWorkOrderEnvelope(
		thread.ThreadID,
		"msg_work_order_verified",
		"idem_work_order_verified",
		pendingEnvelope.SentAt.Add(2*time.Minute),
	)
	if err := st.FinalizePromotion(
		t.Context(),
		record.PendingMessageID,
		pendingEnvelope.SentAt.Add(3*time.Minute),
		verifiedDiagnosis,
		verifiedWorkOrder,
	); err != nil {
		t.Fatalf("FinalizePromotion() error = %v", err)
	}

	snapshot, err = st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() after finalize error = %v", err)
	}
	if len(snapshot.PendingPromotions) != 0 {
		t.Fatalf("expected no active pending promotions after finalize, got %#v", snapshot.PendingPromotions)
	}
	if len(snapshot.Envelopes) != 2 {
		t.Fatalf("expected two verified promotion envelopes, got %#v", snapshot.Envelopes)
	}
	for _, envelope := range snapshot.Envelopes {
		if envelope.Trace.PromotionStatus != model.PromotionStatusPassed {
			t.Fatalf("expected finalized promotion status passed, got %#v", envelope.Trace)
		}
	}
}

func TestFinalizePromotionRejectsIncompleteEnvelopeSet(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	pendingEnvelope := sampleEnvelope(thread.ThreadID, "msg_diag", "idem_diag")
	pendingEnvelope.Type = model.MessageTypeDiagnosisPropose
	pendingEnvelope.Trace.Verified = true
	pendingEnvelope.Trace.PromotionStatus = model.PromotionStatusPending
	record := PromotionPendingRecord{
		PendingMessageID: "pending_msg_diag",
		Envelope:         pendingEnvelope,
		Status:           model.PromotionStatusPending,
		UpdatedAt:        pendingEnvelope.SentAt,
	}
	if err := st.RecordPromotionPending(t.Context(), record); err != nil {
		t.Fatalf("RecordPromotionPending() error = %v", err)
	}

	verifiedDiagnosis := promotedDiagnosisEnvelope(
		thread.ThreadID,
		"msg_diag_verified",
		"idem_diag_verified",
		pendingEnvelope.SentAt.Add(time.Minute),
	)
	if err := st.FinalizePromotion(
		t.Context(),
		record.PendingMessageID,
		pendingEnvelope.SentAt.Add(2*time.Minute),
		verifiedDiagnosis,
	); err == nil {
		t.Fatal("expected FinalizePromotion() to reject diagnosis-only finalization")
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.PendingPromotions) != 1 {
		t.Fatalf("expected pending promotion lane to remain active after rejected finalize, got %#v", snapshot.PendingPromotions)
	}
	if len(snapshot.Envelopes) != 0 {
		t.Fatalf("expected no verified envelopes after rejected finalize, got %#v", snapshot.Envelopes)
	}
}

func TestFinalizePromotionRejectsNonPassedEnvelope(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	pendingEnvelope := sampleEnvelope(thread.ThreadID, "msg_diag", "idem_diag")
	pendingEnvelope.Type = model.MessageTypeDiagnosisPropose
	pendingEnvelope.Trace.Verified = true
	pendingEnvelope.Trace.PromotionStatus = model.PromotionStatusPending
	record := PromotionPendingRecord{
		PendingMessageID: "pending_msg_diag",
		Envelope:         pendingEnvelope,
		Status:           model.PromotionStatusPending,
		UpdatedAt:        pendingEnvelope.SentAt,
	}
	if err := st.RecordPromotionPending(t.Context(), record); err != nil {
		t.Fatalf("RecordPromotionPending() error = %v", err)
	}

	verifiedDiagnosis := promotedDiagnosisEnvelope(
		thread.ThreadID,
		"msg_diag_verified",
		"idem_diag_verified",
		pendingEnvelope.SentAt.Add(time.Minute),
	)
	invalidWorkOrder := promotedWorkOrderEnvelope(
		thread.ThreadID,
		"msg_work_order_verified",
		"idem_work_order_verified",
		pendingEnvelope.SentAt.Add(2*time.Minute),
	)
	invalidWorkOrder.Trace.PromotionStatus = ""
	if err := st.FinalizePromotion(
		t.Context(),
		record.PendingMessageID,
		pendingEnvelope.SentAt.Add(3*time.Minute),
		verifiedDiagnosis,
		invalidWorkOrder,
	); err == nil {
		t.Fatal("expected FinalizePromotion() to reject envelopes without passed promotion status")
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.PendingPromotions) != 1 {
		t.Fatalf("expected pending promotion lane to remain active after rejected finalize, got %#v", snapshot.PendingPromotions)
	}
	if len(snapshot.Envelopes) != 0 {
		t.Fatalf("expected no verified envelopes after rejected finalize, got %#v", snapshot.Envelopes)
	}
}

func TestFinalizePromotionRejectsUnverifiedPassedEnvelope(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	pendingEnvelope := sampleEnvelope(thread.ThreadID, "msg_diag", "idem_diag")
	pendingEnvelope.Type = model.MessageTypeDiagnosisPropose
	pendingEnvelope.Trace.Verified = true
	pendingEnvelope.Trace.PromotionStatus = model.PromotionStatusPending
	record := PromotionPendingRecord{
		PendingMessageID: "pending_msg_diag",
		Envelope:         pendingEnvelope,
		Status:           model.PromotionStatusPending,
		UpdatedAt:        pendingEnvelope.SentAt,
	}
	if err := st.RecordPromotionPending(t.Context(), record); err != nil {
		t.Fatalf("RecordPromotionPending() error = %v", err)
	}

	verifiedDiagnosis := promotedDiagnosisEnvelope(
		thread.ThreadID,
		"msg_diag_verified",
		"idem_diag_verified",
		pendingEnvelope.SentAt.Add(time.Minute),
	)
	unverifiedWorkOrder := promotedWorkOrderEnvelope(
		thread.ThreadID,
		"msg_work_order_verified",
		"idem_work_order_verified",
		pendingEnvelope.SentAt.Add(2*time.Minute),
	)
	unverifiedWorkOrder.Trace.Verified = false
	if err := st.FinalizePromotion(
		t.Context(),
		record.PendingMessageID,
		pendingEnvelope.SentAt.Add(3*time.Minute),
		verifiedDiagnosis,
		unverifiedWorkOrder,
	); err == nil {
		t.Fatal("expected FinalizePromotion() to reject passed envelopes without trace.verified=true")
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.PendingPromotions) != 1 {
		t.Fatalf("expected pending promotion lane to remain active after rejected finalize, got %#v", snapshot.PendingPromotions)
	}
	if len(snapshot.Envelopes) != 0 {
		t.Fatalf("expected no verified envelopes after rejected finalize, got %#v", snapshot.Envelopes)
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

func promotedDiagnosisEnvelope(
	threadID string,
	messageID string,
	idempotencyKey string,
	at time.Time,
) model.Envelope {
	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       threadID,
		From:           "agent.investigator",
		To:             []string{"service.hivebus"},
		Type:           model.MessageTypeDiagnosisPropose,
		Payload:        json.RawMessage(`{"problem":"promotion integrity","likely_cause":"finalize path","proposed_remediation":["require complete verified pair"],"evidence_ids":["art_smoke_log"],"confidence":"high","verified":true}`),
		SentAt:         at,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID:   threadID,
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + messageID,
		},
	}
}

func promotedWorkOrderEnvelope(
	threadID string,
	messageID string,
	idempotencyKey string,
	at time.Time,
) model.Envelope {
	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       threadID,
		From:           "service.hivebus",
		To:             []string{"service.workledger"},
		Type:           model.MessageTypeWorkOrderCreate,
		Payload:        json.RawMessage(`{"tracking_system":"workledger","workledger_project":"hivebus","work_order_id":65,"work_order_title":"FinalizePromotion must require a complete verified promotion set","source_thread_id":"` + threadID + `","optional_sync_targets":["hiveram.com"],"confidence":"high","evidence_ids":["art_smoke_log"]}`),
		SentAt:         at,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID:   threadID,
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + messageID,
		},
	}
}
