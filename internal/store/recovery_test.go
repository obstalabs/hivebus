package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

func TestBuildPromotedThreadRecoveryCapsuleUsesVerifiedProvenanceOnly(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	artifact := model.Artifact{
		ArtifactID:  "art_smoke_log",
		Name:        "smoke.log",
		Kind:        "log",
		URI:         "artifact://sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SizeBytes:   512,
		ContentType: "text/plain",
		Redacted:    true,
	}
	if err := st.AppendArtifact(t.Context(), thread.ThreadID, artifact, formatTime(thread.CreatedAt.Add(time.Minute))); err != nil {
		t.Fatalf("AppendArtifact() error = %v", err)
	}

	appendTestEnvelope(t, st, model.Envelope{
		MessageID:      "msg_stale_fact",
		ThreadID:       thread.ThreadID,
		From:           "agent.investigator",
		To:             []string{"service.hivebus"},
		Type:           model.MessageTypeTaskResultPart,
		Payload:        json.RawMessage(`{"recovery_facts":[{"state":"rejected","text":"ssh host was the old disposable VM"}]}`),
		SentAt:         thread.CreatedAt.Add(2 * time.Minute),
		IdempotencyKey: "idem_stale_fact",
		Trace: model.Trace{
			CorrelationID: thread.ThreadID,
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_stale_fact",
		},
	})
	appendTestEnvelope(t, st, model.Envelope{
		MessageID:      "msg_unlabeled_history",
		ThreadID:       thread.ThreadID,
		From:           "agent.investigator",
		To:             []string{"service.hivebus"},
		Type:           model.MessageTypeTaskResultPart,
		Payload:        json.RawMessage(`{"hypothesis":"raw unlabeled hypothesis should not be replayed"}`),
		SentAt:         thread.CreatedAt.Add(3 * time.Minute),
		IdempotencyKey: "idem_unlabeled_history",
		Trace: model.Trace{
			CorrelationID: thread.ThreadID,
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_unlabeled_history",
		},
	})

	diagnosis := model.Diagnosis{
		Problem:             "Codex loses smoke VM context after compaction.",
		LikelyCause:         "Only raw thread history preserved the VM handoff, so the current task drifted after compaction.",
		ProposedRemediation: []string{"Publish a verified recovery capsule with explicit stale-fact labels."},
		EvidenceIDs:         []string{artifact.ArtifactID},
		Confidence:          model.ConfidenceHigh,
		Verified:            true,
	}
	appendTestEnvelope(t, st, diagnosisEnvelope(t, thread.ThreadID, diagnosis, thread.CreatedAt.Add(4*time.Minute)))
	appendTestEnvelope(t, st, workOrderEnvelope(thread.ThreadID, thread.CreatedAt.Add(5*time.Minute)))

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	capsule, err := BuildPromotedThreadRecoveryCapsule(
		snapshot,
		time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("BuildPromotedThreadRecoveryCapsule() error = %v", err)
	}

	if capsule.Type != model.RecoveryCapsuleTypePromotedThread {
		t.Fatalf("unexpected capsule type %q", capsule.Type)
	}
	if !capsule.VerifiedDiagnosis.Verified {
		t.Fatal("expected verified diagnosis summary")
	}
	if capsule.Promotion.WorkOrderID != 44 {
		t.Fatalf("expected promotion WO-44, got %#v", capsule.Promotion)
	}
	if len(capsule.Evidence) != 1 || capsule.Evidence[0].ArtifactID != artifact.ArtifactID {
		t.Fatalf("expected evidence ref for %q, got %#v", artifact.ArtifactID, capsule.Evidence)
	}
	if len(capsule.RejectedOrStale) != 1 {
		t.Fatalf("expected one rejected fact, got %#v", capsule.RejectedOrStale)
	}
	if capsule.RejectedOrStale[0].State != model.RecoveryFactRejected {
		t.Fatalf("expected rejected fact label, got %#v", capsule.RejectedOrStale[0])
	}
	if capsule.MissingInfoResolution.Status != "resolved" {
		t.Fatalf("expected resolved missing info, got %#v", capsule.MissingInfoResolution)
	}

	body := string(marshalJSONForTest(t, capsule))
	if strings.Contains(body, "raw unlabeled hypothesis") {
		t.Fatalf("capsule leaked unlabeled raw history: %s", body)
	}
	if strings.Contains(body, artifact.URI) {
		t.Fatalf("capsule leaked artifact URI instead of compact ref: %s", body)
	}
}

func TestBuildPromotedThreadRecoveryCapsuleRequiresPromotionAndResolvedDiagnosis(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	diagnosis := model.Diagnosis{
		Problem:             "Smoke lane blocked.",
		LikelyCause:         "Missing VM context.",
		ProposedRemediation: []string{"Ask operator for current VM."},
		MissingInfo:         []string{"current VM address"},
		EvidenceIDs:         []string{"art_missing"},
		Confidence:          model.ConfidenceMedium,
		Verified:            true,
	}
	appendTestEnvelope(t, st, diagnosisEnvelope(t, thread.ThreadID, diagnosis, thread.CreatedAt.Add(time.Minute)))

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if _, err := BuildPromotedThreadRecoveryCapsule(snapshot, thread.CreatedAt); err == nil {
		t.Fatal("expected unresolved missing_info to block recovery capsule")
	}

	diagnosis.MissingInfo = nil
	appendTestEnvelope(t, st, diagnosisEnvelopeWithID(
		t,
		thread.ThreadID,
		diagnosis,
		"msg_diagnosis_resolved",
		"idem_diagnosis_resolved",
		thread.CreatedAt.Add(2*time.Minute),
	))
	snapshot, err = st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread(resolved) error = %v", err)
	}
	if _, err := BuildPromotedThreadRecoveryCapsule(snapshot, thread.CreatedAt); err == nil {
		t.Fatal("expected missing promotion receipt to block recovery capsule")
	}
}

func TestBuildPromotedThreadRecoveryCapsuleIgnoresPendingPromotionLane(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	diagnosis := model.Diagnosis{
		Problem:             "Smoke lane blocked.",
		LikelyCause:         "Pending promotion should not count as verified recovery state.",
		ProposedRemediation: []string{"Finish the promotion or leave it diagnostic only."},
		EvidenceIDs:         []string{"art_smoke_log"},
		Confidence:          model.ConfidenceHigh,
		Verified:            true,
	}
	pending := diagnosisEnvelopeWithID(
		t,
		thread.ThreadID,
		diagnosis,
		"msg_diagnosis_pending",
		"idem_diagnosis_pending",
		thread.CreatedAt.Add(time.Minute),
	)
	pending.Trace.PromotionStatus = model.PromotionStatusFailed
	if err := st.RecordPromotionPending(t.Context(), PromotionPendingRecord{
		PendingMessageID: "pending_msg_diagnosis_pending",
		Envelope:         pending,
		Status:           model.PromotionStatusFailed,
		FailureReason:    "missing_info still unresolved",
		UpdatedAt:        thread.CreatedAt.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("RecordPromotionPending() error = %v", err)
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.PendingPromotions) != 1 {
		t.Fatalf("expected one pending promotion record, got %#v", snapshot.PendingPromotions)
	}
	if _, err := BuildPromotedThreadRecoveryCapsule(snapshot, thread.CreatedAt); err == nil {
		t.Fatal("expected pending-only promotion lane to be ignored by recovery")
	}
}

func TestBuildPromotedThreadRecoveryCapsuleAcceptsLegacyPromotedEnvelopes(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	diagnosis := model.Diagnosis{
		Problem:             "Smoke lane blocked.",
		LikelyCause:         "Legacy promoted envelopes predate promotion_status.",
		ProposedRemediation: []string{"Keep legacy recovery alive until WO-61 migration lands."},
		EvidenceIDs:         []string{"art_smoke_log"},
		Confidence:          model.ConfidenceHigh,
		Verified:            true,
	}
	legacyDiagnosis := diagnosisEnvelope(t, thread.ThreadID, diagnosis, thread.CreatedAt.Add(time.Minute))
	legacyDiagnosis.Trace.PromotionStatus = ""
	appendTestEnvelope(t, st, legacyDiagnosis)

	legacyPromotion := workOrderEnvelope(thread.ThreadID, thread.CreatedAt.Add(2*time.Minute))
	legacyPromotion.Trace.PromotionStatus = ""
	appendTestEnvelope(t, st, legacyPromotion)

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	capsule, err := BuildPromotedThreadRecoveryCapsule(snapshot, thread.CreatedAt.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("BuildPromotedThreadRecoveryCapsule() error = %v", err)
	}
	if capsule.VerifiedDiagnosis.SourceMessageID != legacyDiagnosis.MessageID {
		t.Fatalf("expected legacy diagnosis to remain recoverable, got %#v", capsule.VerifiedDiagnosis)
	}
	if capsule.Promotion.SourceMessageID != legacyPromotion.MessageID {
		t.Fatalf("expected legacy promotion receipt to remain recoverable, got %#v", capsule.Promotion)
	}
}

func TestBuildPromotedThreadRecoveryCapsulePrefersExplicitPromotionStatusOverLegacyFallback(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	legacyDiagnosis := diagnosisEnvelopeWithID(
		t,
		thread.ThreadID,
		model.Diagnosis{
			Problem:             "Legacy promoted diagnosis.",
			LikelyCause:         "This should be ignored once explicit promotion status exists.",
			ProposedRemediation: []string{"Prefer the explicit passed promotion state."},
			EvidenceIDs:         []string{"art_smoke_log"},
			Confidence:          model.ConfidenceMedium,
			Verified:            true,
		},
		"msg_diagnosis_legacy",
		"idem_diagnosis_legacy",
		thread.CreatedAt.Add(time.Minute),
	)
	legacyDiagnosis.Trace.PromotionStatus = ""
	appendTestEnvelope(t, st, legacyDiagnosis)

	legacyPromotion := workOrderEnvelope(thread.ThreadID, thread.CreatedAt.Add(2*time.Minute))
	legacyPromotion.MessageID = "msg_work_order_legacy"
	legacyPromotion.IdempotencyKey = "idem_work_order_legacy"
	legacyPromotion.Trace.PromotionStatus = ""
	appendTestEnvelope(t, st, legacyPromotion)

	explicitDiagnosis := diagnosisEnvelopeWithID(
		t,
		thread.ThreadID,
		model.Diagnosis{
			Problem:             "Explicit promoted diagnosis.",
			LikelyCause:         "Promotion passed with explicit status.",
			ProposedRemediation: []string{"Use this diagnosis instead of the legacy one."},
			EvidenceIDs:         []string{"art_smoke_log"},
			Confidence:          model.ConfidenceHigh,
			Verified:            true,
		},
		"msg_diagnosis_passed",
		"idem_diagnosis_passed",
		thread.CreatedAt.Add(3*time.Minute),
	)
	appendTestEnvelope(t, st, explicitDiagnosis)

	explicitPromotion := workOrderEnvelope(thread.ThreadID, thread.CreatedAt.Add(4*time.Minute))
	explicitPromotion.MessageID = "msg_work_order_passed"
	explicitPromotion.IdempotencyKey = "idem_work_order_passed"
	explicitPromotion.Payload = json.RawMessage(`{
		"tracking_system":"workledger",
		"workledger_project":"hivebus",
		"work_order_id":61,
		"work_order_title":"Explicit promotion wins",
		"source_thread_id":"` + thread.ThreadID + `",
		"optional_sync_targets":["hiveram.com"],
		"confidence":"high",
		"evidence_ids":["art_smoke_log"]
	}`)
	appendTestEnvelope(t, st, explicitPromotion)

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	capsule, err := BuildPromotedThreadRecoveryCapsule(snapshot, thread.CreatedAt.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("BuildPromotedThreadRecoveryCapsule() error = %v", err)
	}
	if capsule.VerifiedDiagnosis.SourceMessageID != explicitDiagnosis.MessageID {
		t.Fatalf("expected explicit diagnosis to win over legacy fallback, got %#v", capsule.VerifiedDiagnosis)
	}
	if capsule.Promotion.SourceMessageID != explicitPromotion.MessageID {
		t.Fatalf("expected explicit promotion receipt to win over legacy fallback, got %#v", capsule.Promotion)
	}
	if capsule.Promotion.WorkOrderID != 61 {
		t.Fatalf("expected explicit work order id 61, got %#v", capsule.Promotion)
	}
}

func TestBuildPromotedThreadRecoveryCapsuleKeepsLegacyFallbackForUnverifiedExplicitStatus(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	legacyDiagnosis := diagnosisEnvelopeWithID(
		t,
		thread.ThreadID,
		model.Diagnosis{
			Problem:             "Legacy promoted diagnosis.",
			LikelyCause:         "Unverified explicit status must not close the compatibility window.",
			ProposedRemediation: []string{"Keep the legacy fallback until authoritative passed evidence exists."},
			EvidenceIDs:         []string{"art_smoke_log"},
			Confidence:          model.ConfidenceMedium,
			Verified:            true,
		},
		"msg_diagnosis_legacy_unverified",
		"idem_diagnosis_legacy_unverified",
		thread.CreatedAt.Add(time.Minute),
	)
	legacyDiagnosis.Trace.PromotionStatus = ""
	appendTestEnvelope(t, st, legacyDiagnosis)

	legacyPromotion := workOrderEnvelope(thread.ThreadID, thread.CreatedAt.Add(2*time.Minute))
	legacyPromotion.MessageID = "msg_work_order_legacy_unverified"
	legacyPromotion.IdempotencyKey = "idem_work_order_legacy_unverified"
	legacyPromotion.Trace.PromotionStatus = ""
	appendTestEnvelope(t, st, legacyPromotion)

	unverifiedPassedDiagnosis := diagnosisEnvelopeWithID(
		t,
		thread.ThreadID,
		model.Diagnosis{
			Problem:             "Unverified explicit status.",
			LikelyCause:         "A general append path should not close legacy recovery.",
			ProposedRemediation: []string{"Ignore unverified explicit status for legacy gating."},
			EvidenceIDs:         []string{"art_smoke_log"},
			Confidence:          model.ConfidenceLow,
			Verified:            true,
		},
		"msg_diagnosis_unverified_passed",
		"idem_diagnosis_unverified_passed",
		thread.CreatedAt.Add(3*time.Minute),
	)
	unverifiedPassedDiagnosis.Trace.Verified = false
	appendTestEnvelope(t, st, unverifiedPassedDiagnosis)

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	capsule, err := BuildPromotedThreadRecoveryCapsule(snapshot, thread.CreatedAt.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("BuildPromotedThreadRecoveryCapsule() error = %v", err)
	}
	if capsule.VerifiedDiagnosis.SourceMessageID != legacyDiagnosis.MessageID {
		t.Fatalf("expected legacy diagnosis to remain active, got %#v", capsule.VerifiedDiagnosis)
	}
	if capsule.Promotion.SourceMessageID != legacyPromotion.MessageID {
		t.Fatalf("expected legacy promotion receipt to remain active, got %#v", capsule.Promotion)
	}
}

func TestBuildPromotedThreadRecoveryCapsuleKeepsLegacyFallbackForFailedOrPendingStatus(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	legacyDiagnosis := diagnosisEnvelopeWithID(
		t,
		thread.ThreadID,
		model.Diagnosis{
			Problem:             "Legacy promoted diagnosis.",
			LikelyCause:         "Failed or pending explicit status must not close the compatibility window.",
			ProposedRemediation: []string{"Keep the legacy fallback until a passed promotion exists."},
			EvidenceIDs:         []string{"art_smoke_log"},
			Confidence:          model.ConfidenceMedium,
			Verified:            true,
		},
		"msg_diagnosis_legacy_failed",
		"idem_diagnosis_legacy_failed",
		thread.CreatedAt.Add(time.Minute),
	)
	legacyDiagnosis.Trace.PromotionStatus = ""
	appendTestEnvelope(t, st, legacyDiagnosis)

	legacyPromotion := workOrderEnvelope(thread.ThreadID, thread.CreatedAt.Add(2*time.Minute))
	legacyPromotion.MessageID = "msg_work_order_legacy_failed"
	legacyPromotion.IdempotencyKey = "idem_work_order_legacy_failed"
	legacyPromotion.Trace.PromotionStatus = ""
	appendTestEnvelope(t, st, legacyPromotion)

	failedDiagnosis := diagnosisEnvelopeWithID(
		t,
		thread.ThreadID,
		model.Diagnosis{
			Problem:             "Failed explicit status.",
			LikelyCause:         "Promotion failed after emitting a diagnostic envelope.",
			ProposedRemediation: []string{"Do not let failed status disable legacy recovery."},
			EvidenceIDs:         []string{"art_smoke_log"},
			Confidence:          model.ConfidenceLow,
			Verified:            true,
		},
		"msg_diagnosis_failed_explicit",
		"idem_diagnosis_failed_explicit",
		thread.CreatedAt.Add(3*time.Minute),
	)
	failedDiagnosis.Trace.PromotionStatus = model.PromotionStatusFailed
	appendTestEnvelope(t, st, failedDiagnosis)

	pendingPromotion := workOrderEnvelope(thread.ThreadID, thread.CreatedAt.Add(4*time.Minute))
	pendingPromotion.MessageID = "msg_work_order_pending_explicit"
	pendingPromotion.IdempotencyKey = "idem_work_order_pending_explicit"
	pendingPromotion.Trace.PromotionStatus = model.PromotionStatusPending
	appendTestEnvelope(t, st, pendingPromotion)

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	capsule, err := BuildPromotedThreadRecoveryCapsule(snapshot, thread.CreatedAt.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("BuildPromotedThreadRecoveryCapsule() error = %v", err)
	}
	if capsule.VerifiedDiagnosis.SourceMessageID != legacyDiagnosis.MessageID {
		t.Fatalf("expected legacy diagnosis to remain active, got %#v", capsule.VerifiedDiagnosis)
	}
	if capsule.Promotion.SourceMessageID != legacyPromotion.MessageID {
		t.Fatalf("expected legacy promotion receipt to remain active, got %#v", capsule.Promotion)
	}
}

func appendTestEnvelope(t *testing.T, st *Store, envelope model.Envelope) {
	t.Helper()

	if envelope.Trace.Verified && isPromotionRecoveryEnvelopeType(envelope.Type) {
		switch envelope.Trace.PromotionStatus {
		case "":
			appendLegacyPromotionEnvelopeForTest(t, st, envelope)
			return
		case model.PromotionStatusPassed:
			appendAuthoritativePromotionEnvelopeForTest(t, st, envelope)
			return
		}
	}

	if err := st.AppendEnvelope(t.Context(), envelope); err != nil {
		t.Fatalf("AppendEnvelope(%s) error = %v", envelope.MessageID, err)
	}
}

func appendLegacyPromotionEnvelopeForTest(t *testing.T, st *Store, envelope model.Envelope) {
	t.Helper()

	tx, err := st.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := insertEnvelopeEventTx(t.Context(), tx, envelope); err != nil {
		t.Fatalf("insertEnvelopeEventTx(%s) error = %v", envelope.MessageID, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
}

func appendAuthoritativePromotionEnvelopeForTest(t *testing.T, st *Store, envelope model.Envelope) {
	t.Helper()

	pendingEnvelope := envelope
	pendingEnvelope.MessageID = "pending_" + envelope.MessageID
	pendingEnvelope.IdempotencyKey = "pending_" + envelope.IdempotencyKey
	pendingEnvelope.Trace.PromotionStatus = model.PromotionStatusPending

	record := PromotionPendingRecord{
		PendingMessageID: "pending_record_" + envelope.MessageID,
		Envelope:         pendingEnvelope,
		Status:           model.PromotionStatusPending,
		UpdatedAt:        envelope.SentAt,
	}
	if err := st.RecordPromotionPending(t.Context(), record); err != nil {
		t.Fatalf("RecordPromotionPending(%s) error = %v", envelope.MessageID, err)
	}
	if err := st.FinalizePromotion(
		t.Context(),
		record.PendingMessageID,
		envelope.SentAt.Add(time.Nanosecond),
		envelope,
	); err != nil {
		t.Fatalf("FinalizePromotion(%s) error = %v", envelope.MessageID, err)
	}
}

func diagnosisEnvelope(
	t *testing.T,
	threadID string,
	diagnosis model.Diagnosis,
	at time.Time,
) model.Envelope {
	t.Helper()

	return diagnosisEnvelopeWithID(t, threadID, diagnosis, "msg_diagnosis", "idem_diagnosis", at)
}

func diagnosisEnvelopeWithID(
	t *testing.T,
	threadID string,
	diagnosis model.Diagnosis,
	messageID string,
	idempotencyKey string,
	at time.Time,
) model.Envelope {
	t.Helper()

	payload := marshalJSONForTest(t, diagnosis)
	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       threadID,
		From:           "agent.investigator",
		To:             []string{"service.hivebus"},
		Type:           model.MessageTypeDiagnosisPropose,
		Payload:        payload,
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

func workOrderEnvelope(threadID string, at time.Time) model.Envelope {
	return model.Envelope{
		MessageID: "msg_work_order",
		ThreadID:  threadID,
		From:      "service.hivebus",
		To:        []string{"service.workledger"},
		Type:      model.MessageTypeWorkOrderCreate,
		Payload: json.RawMessage(`{
			"tracking_system":"workledger",
			"workledger_project":"hivebus",
			"work_order_id":44,
			"work_order_title":"Promoted thread recovery capsule",
			"source_thread_id":"` + threadID + `",
			"optional_sync_targets":["hiveram.com"],
			"confidence":"high",
			"evidence_ids":["art_smoke_log"]
		}`),
		SentAt:         at,
		IdempotencyKey: "idem_work_order",
		Trace: model.Trace{
			CorrelationID:   threadID,
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_work_order",
		},
	}
}

func marshalJSONForTest(t *testing.T, value any) json.RawMessage {
	t.Helper()

	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return payload
}
