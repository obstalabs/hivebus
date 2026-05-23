package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/store"
	"github.com/ppiankov/hivebus/internal/work"
)

func TestPromoteThreadFailureLeavesOnlyPendingPromotionRecord(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	bridge := &fakeWorkOrderBridge{}
	handler := NewHandlerWithOptions(
		st,
		openTestArtifactStore(t),
		keys,
		HandlerOptions{WorkOrders: bridge},
	)

	thread := sampleThread()
	thread.Status = model.ThreadStatusInvestigating
	mustSeedThread(t, st, thread)

	reqBody := marshalJSON(t, promoteThreadRequest{
		RequestedBy:       "agent.investigator",
		WorkledgerProject: "neurorouter-pro",
		Diagnosis: model.Diagnosis{
			Problem:             "Clawbot smoke lane is unstable during release validation.",
			LikelyCause:         "The disposable ARM64 VM lost the current host handoff.",
			ProposedRemediation: []string{"Restore the current smoke VM identity."},
			MissingInfo:         []string{"current smoke VM address"},
			EvidenceIDs:         []string{"art_smoke_log"},
			Confidence:          model.ConfidenceHigh,
			Verified:            true,
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v0/threads/"+thread.ThreadID+"/promote", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /v0/threads/{id}/promote status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if len(bridge.drafts) != 0 {
		t.Fatalf("expected no workledger drafts on failed promotion, got %d", len(bridge.drafts))
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.Envelopes) != 0 {
		t.Fatalf("expected no verified envelopes after failed promotion, got %#v", snapshot.Envelopes)
	}
	if len(snapshot.PendingPromotions) != 1 {
		t.Fatalf("expected one failed pending promotion record, got %#v", snapshot.PendingPromotions)
	}
	if snapshot.PendingPromotions[0].Status != model.PromotionStatusFailed {
		t.Fatalf("expected failed pending promotion status, got %#v", snapshot.PendingPromotions[0])
	}
	if snapshot.PendingPromotions[0].Envelope.Trace.PromotionStatus != model.PromotionStatusFailed {
		t.Fatalf("expected failed promotion trace status, got %#v", snapshot.PendingPromotions[0].Envelope.Trace)
	}

	recoveryReq := httptest.NewRequest(http.MethodGet, "/v0/threads/"+thread.ThreadID+"/recovery", nil)
	recoveryReq.Header.Set("Authorization", "Bearer operator-secret")
	recoveryRec := httptest.NewRecorder()
	handler.ServeHTTP(recoveryRec, recoveryReq)
	if recoveryRec.Code != http.StatusBadRequest {
		t.Fatalf("GET /v0/threads/{id}/recovery status = %d, body = %s", recoveryRec.Code, recoveryRec.Body.String())
	}
}

func TestPromoteThreadBridgeFailureDoesNotPersistVerifiedPromotionState(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	bridge := &fakeWorkOrderBridge{err: errors.New("workledger bridge unavailable")}
	handler := NewHandlerWithOptions(
		st,
		openTestArtifactStore(t),
		keys,
		HandlerOptions{WorkOrders: bridge},
	)

	thread := sampleThread()
	thread.Status = model.ThreadStatusInvestigating
	mustSeedThread(t, st, thread)

	reqBody := marshalJSON(t, promoteThreadRequest{
		RequestedBy:       "agent.investigator",
		WorkledgerProject: "neurorouter-pro",
		Diagnosis: model.Diagnosis{
			Problem:             "Clawbot smoke lane is unstable during release validation.",
			LikelyCause:         "The disposable ARM64 VM lost the current host handoff.",
			ProposedRemediation: []string{"Restore the current smoke VM identity."},
			EvidenceIDs:         []string{"art_smoke_log"},
			Confidence:          model.ConfidenceHigh,
			Verified:            true,
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v0/threads/"+thread.ThreadID+"/promote", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("POST /v0/threads/{id}/promote status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if len(bridge.drafts) != 1 {
		t.Fatalf("expected one attempted workledger draft, got %d", len(bridge.drafts))
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.Envelopes) != 0 {
		t.Fatalf("expected no verified envelopes after bridge failure, got %#v", snapshot.Envelopes)
	}
	if len(snapshot.PendingPromotions) != 1 {
		t.Fatalf("expected one failed pending promotion record, got %#v", snapshot.PendingPromotions)
	}
	if snapshot.PendingPromotions[0].Status != model.PromotionStatusFailed {
		t.Fatalf("expected failed pending promotion status, got %#v", snapshot.PendingPromotions[0])
	}
}

func TestPromoteThreadSuccessClearsPendingPromotionLane(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	bridge := &fakeWorkOrderBridge{
		ref: WorkOrderRef{
			Project: "neurorouter-pro",
			ID:      201,
			Title:   "Resolve: Clawbot smoke lane is unstable",
		},
	}
	handler := NewHandlerWithOptions(
		st,
		openTestArtifactStore(t),
		keys,
		HandlerOptions{WorkOrders: bridge},
	)

	thread := sampleThread()
	thread.Status = model.ThreadStatusInvestigating
	mustSeedThread(t, st, thread)

	reqBody := marshalJSON(t, promoteThreadRequest{
		RequestedBy:       "agent.investigator",
		WorkledgerProject: "neurorouter-pro",
		Diagnosis: model.Diagnosis{
			Problem:             "Clawbot smoke lane is unstable during release validation.",
			LikelyCause:         "The disposable ARM64 VM lost the current host handoff.",
			ProposedRemediation: []string{"Restore the current smoke VM identity."},
			EvidenceIDs:         []string{"art_smoke_log"},
			Confidence:          model.ConfidenceHigh,
			Verified:            true,
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v0/threads/"+thread.ThreadID+"/promote", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v0/threads/{id}/promote status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp promoteThreadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal(promote) error = %v", err)
	}
	if len(resp.Snapshot.PendingPromotions) != 0 {
		t.Fatalf("expected no active pending promotions after successful finalize, got %#v", resp.Snapshot.PendingPromotions)
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.PendingPromotions) != 0 {
		t.Fatalf("expected pending promotion lane to be resolved, got %#v", snapshot.PendingPromotions)
	}
	if len(snapshot.Envelopes) != 2 {
		t.Fatalf("expected diagnosis + work order verified envelopes, got %#v", snapshot.Envelopes)
	}
	if snapshot.Envelopes[0].Trace.PromotionStatus != model.PromotionStatusPassed {
		t.Fatalf("expected diagnosis envelope promotion status passed, got %#v", snapshot.Envelopes[0].Trace)
	}
	if snapshot.Envelopes[1].Trace.PromotionStatus != model.PromotionStatusPassed {
		t.Fatalf("expected work order envelope promotion status passed, got %#v", snapshot.Envelopes[1].Trace)
	}
}

func TestPromoteThreadRetryAfterSuccessfulPromotionReturnsDuplicateNoOp(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	bridge := &fakeWorkOrderBridge{
		ref: WorkOrderRef{
			Project: "neurorouter-pro",
			ID:      201,
			Title:   "Resolve: Clawbot smoke lane is unstable",
		},
	}
	handler := NewHandlerWithOptions(
		st,
		openTestArtifactStore(t),
		keys,
		HandlerOptions{WorkOrders: bridge},
	)

	thread := sampleThread()
	thread.Status = model.ThreadStatusInvestigating
	mustSeedThread(t, st, thread)

	reqBody := marshalJSON(t, validPromoteThreadRequestForRetryTest())
	status, resp, raw := postPromoteForRetryTest(t, handler, thread.ThreadID, reqBody)
	if status != http.StatusCreated {
		t.Fatalf("first promote status = %d, body = %s", status, raw)
	}

	status, resp, raw = postPromoteForRetryTest(t, handler, thread.ThreadID, reqBody)
	if status != http.StatusOK {
		t.Fatalf("retry promote status = %d, body = %s", status, raw)
	}
	if resp.Status != "duplicate" {
		t.Fatalf("expected duplicate retry status, got %#v", resp)
	}
	if resp.Canonical.ID != 201 {
		t.Fatalf("expected canonical work order 201, got %#v", resp.Canonical)
	}
	if len(bridge.drafts) != 1 {
		t.Fatalf("expected one workledger side effect across retry, got %d", len(bridge.drafts))
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.PendingPromotions) != 0 {
		t.Fatalf("expected no pending promotion records after duplicate retry, got %#v", snapshot.PendingPromotions)
	}
	if len(snapshot.Envelopes) != 2 {
		t.Fatalf("expected one finalized promotion pair, got %#v", snapshot.Envelopes)
	}
}

func TestPromoteThreadRetryUsesExistingVerifiedPromotionWithoutBridge(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	bridge := &fakeWorkOrderBridge{err: errors.New("bridge should not be called")}
	handler := NewHandlerWithOptions(
		st,
		openTestArtifactStore(t),
		keys,
		HandlerOptions{WorkOrders: bridge},
	)

	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	mustSeedThread(t, st, thread)

	request := validPromoteThreadRequestForRetryTest()
	canonical := WorkOrderRef{
		Project: "neurorouter-pro",
		ID:      301,
		Title:   "Resolve: Clawbot smoke lane is unstable",
	}
	seedSuccessfulPromotionForRetryTest(t, st, thread, request, canonical)

	status, resp, raw := postPromoteForRetryTest(t, handler, thread.ThreadID, marshalJSON(t, request))
	if status != http.StatusOK {
		t.Fatalf("retry promote status = %d, body = %s", status, raw)
	}
	if resp.Status != "duplicate" {
		t.Fatalf("expected duplicate retry status, got %#v", resp)
	}
	if resp.Canonical.ID != canonical.ID {
		t.Fatalf("expected existing canonical work order, got %#v", resp.Canonical)
	}
	if len(bridge.drafts) != 0 {
		t.Fatalf("expected no workledger side effect on verified retry, got %d", len(bridge.drafts))
	}
}

func TestPromoteThreadRetryUsesLegacyReceiptWhenUnambiguous(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	bridge := &fakeWorkOrderBridge{err: errors.New("bridge should not be called")}
	handler := NewHandlerWithOptions(
		st,
		openTestArtifactStore(t),
		keys,
		HandlerOptions{WorkOrders: bridge},
	)

	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	mustSeedThread(t, st, thread)

	request := validPromoteThreadRequestForRetryTest()
	canonical := WorkOrderRef{
		Project: "neurorouter-pro",
		ID:      701,
		Title:   "Resolve: Clawbot smoke lane is unstable",
	}
	seedSuccessfulPromotionWithReceiptMutationForRetryTest(t, st, thread, request, canonical, "", func(receipt *promotedRetryReceipt) {
		receipt.DiagnosisMessageID = ""
		receipt.DiagnosisPayloadSHA256 = ""
	})

	status, resp, raw := postPromoteForRetryTest(t, handler, thread.ThreadID, marshalJSON(t, request))
	if status != http.StatusOK {
		t.Fatalf("retry promote status = %d, body = %s", status, raw)
	}
	if resp.Status != "duplicate" {
		t.Fatalf("expected duplicate retry status, got %#v", resp)
	}
	if resp.Canonical.ID != canonical.ID {
		t.Fatalf("expected existing legacy canonical work order, got %#v", resp.Canonical)
	}
	if len(bridge.drafts) != 0 {
		t.Fatalf("expected no workledger side effect on unambiguous legacy retry, got %d", len(bridge.drafts))
	}
}

// WO-74: legacy receipts have no diagnosis binding fields, so ambiguity must
// fail closed instead of choosing an arbitrary finalized Workledger receipt.
func TestPromoteThreadRetryRejectsAmbiguousLegacyReceipts(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	mustSeedThread(t, st, thread)

	request := validPromoteThreadRequestForRetryTest()
	seedSuccessfulPromotionWithReceiptMutationForRetryTest(t, st, thread, request, WorkOrderRef{
		Project: "neurorouter-pro",
		ID:      701,
		Title:   "Resolve: Clawbot smoke lane is unstable",
	}, "", func(receipt *promotedRetryReceipt) {
		receipt.DiagnosisMessageID = ""
		receipt.DiagnosisPayloadSHA256 = ""
	})

	staleRequest := validPromoteThreadRequestForRetryTest()
	staleRequest.OptionalSyncTargets = []string{"hiveram.com"}
	staleRequest.Diagnosis.Problem = "Clawbot smoke lane investigation drifted to stale evidence."
	staleRequest.Diagnosis.EvidenceIDs = []string{"art_stale_log"}
	staleRequest.Diagnosis.Confidence = model.ConfidenceMedium
	seedSuccessfulPromotionWithReceiptMutationForRetryTest(t, st, thread, staleRequest, WorkOrderRef{
		Project: "neurorouter-pro",
		ID:      999,
		Title:   "Resolve: Clawbot smoke lane is unstable",
	}, "stale", func(receipt *promotedRetryReceipt) {
		receipt.DiagnosisMessageID = ""
		receipt.DiagnosisPayloadSHA256 = ""
	})

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	at := time.Date(2026, 4, 18, 7, 0, 0, 0, time.UTC)
	diagnosisEnvelope, err := buildDiagnosisEnvelope(thread.ThreadID, request, at)
	if err != nil {
		t.Fatalf("buildDiagnosisEnvelope() error = %v", err)
	}
	draft, err := work.DraftFromThread(request.WorkledgerProject, thread, request.Diagnosis)
	if err != nil {
		t.Fatalf("DraftFromThread() error = %v", err)
	}

	if ref, ok := existingPromotedRetry(snapshot, diagnosisEnvelope, draft); ok {
		t.Fatalf("expected ambiguous legacy retry to fail closed, got %#v", ref)
	}
}

// WO-77: malformed passed work-order receipts must make legacy retry recovery
// fail closed instead of being skipped as invisible noise.
func TestPromoteThreadRetryRejectsMalformedLegacyReceipt(t *testing.T) {
	t.Helper()

	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	request := validPromoteThreadRequestForRetryTest()
	at := time.Date(2026, 4, 18, 7, 0, 0, 0, time.UTC)

	draft, err := work.DraftFromThread(request.WorkledgerProject, thread, request.Diagnosis)
	if err != nil {
		t.Fatalf("DraftFromThread() error = %v", err)
	}
	diagnosisEnvelope, err := buildDiagnosisEnvelope(thread.ThreadID, request, at)
	if err != nil {
		t.Fatalf("buildDiagnosisEnvelope() error = %v", err)
	}
	workOrderEnvelope, err := buildWorkOrderEnvelope(thread.ThreadID, request, draft, WorkOrderRef{
		Project: "neurorouter-pro",
		ID:      702,
		Title:   "Resolve: Clawbot smoke lane is unstable",
	}, at.Add(time.Second))
	if err != nil {
		t.Fatalf("buildWorkOrderEnvelope() error = %v", err)
	}
	legacyReceipt := mutatePromotedRetryReceiptForTest(t, workOrderEnvelope, func(receipt *promotedRetryReceipt) {
		receipt.DiagnosisMessageID = ""
		receipt.DiagnosisPayloadSHA256 = ""
	})
	malformedReceipt := suffixPromotionEnvelopeForRetryTest(workOrderEnvelope, "malformed")
	malformedReceipt.Payload = json.RawMessage(`{"tracking_system":`)

	snapshot := store.ThreadSnapshot{
		Thread:    thread,
		Envelopes: []model.Envelope{diagnosisEnvelope, legacyReceipt, malformedReceipt},
	}
	if ref, ok := existingPromotedRetry(snapshot, diagnosisEnvelope, draft); ok {
		t.Fatalf("expected malformed legacy receipt to fail closed, got %#v", ref)
	}
}

// WO-77: partial diagnosis binding is neither a strict receipt nor a legacy
// receipt, so it must make the legacy lane ambiguous when present.
func TestPromoteThreadRetryRejectsPartialBindingLegacyReceipt(t *testing.T) {
	t.Helper()

	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	request := validPromoteThreadRequestForRetryTest()
	at := time.Date(2026, 4, 18, 7, 0, 0, 0, time.UTC)

	draft, err := work.DraftFromThread(request.WorkledgerProject, thread, request.Diagnosis)
	if err != nil {
		t.Fatalf("DraftFromThread() error = %v", err)
	}
	diagnosisEnvelope, err := buildDiagnosisEnvelope(thread.ThreadID, request, at)
	if err != nil {
		t.Fatalf("buildDiagnosisEnvelope() error = %v", err)
	}
	workOrderEnvelope, err := buildWorkOrderEnvelope(thread.ThreadID, request, draft, WorkOrderRef{
		Project: "neurorouter-pro",
		ID:      703,
		Title:   "Resolve: Clawbot smoke lane is unstable",
	}, at.Add(time.Second))
	if err != nil {
		t.Fatalf("buildWorkOrderEnvelope() error = %v", err)
	}
	legacyReceipt := mutatePromotedRetryReceiptForTest(t, workOrderEnvelope, func(receipt *promotedRetryReceipt) {
		receipt.DiagnosisMessageID = ""
		receipt.DiagnosisPayloadSHA256 = ""
	})
	partialReceipt := suffixPromotionEnvelopeForRetryTest(
		mutatePromotedRetryReceiptForTest(t, workOrderEnvelope, func(receipt *promotedRetryReceipt) {
			receipt.DiagnosisPayloadSHA256 = ""
		}),
		"partial",
	)

	snapshot := store.ThreadSnapshot{
		Thread:    thread,
		Envelopes: []model.Envelope{diagnosisEnvelope, legacyReceipt, partialReceipt},
	}
	if ref, ok := existingPromotedRetry(snapshot, diagnosisEnvelope, draft); ok {
		t.Fatalf("expected partial binding receipt to fail closed, got %#v", ref)
	}
}

// WO-72: when several receipts exist, retry reconciliation must choose the
// receipt bound to the retried diagnosis instead of the nearest thread receipt.
func TestPromoteThreadRetryUsesReceiptBoundToMatchingDiagnosis(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	bridge := &fakeWorkOrderBridge{err: errors.New("bridge should not be called")}
	handler := NewHandlerWithOptions(
		st,
		openTestArtifactStore(t),
		keys,
		HandlerOptions{WorkOrders: bridge},
	)

	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	mustSeedThread(t, st, thread)

	request := validPromoteThreadRequestForRetryTest()
	seedSuccessfulPromotionForRetryTest(t, st, thread, request, WorkOrderRef{
		Project: "neurorouter-pro",
		ID:      301,
		Title:   "Resolve: Clawbot smoke lane is unstable",
	})

	staleRequest := validPromoteThreadRequestForRetryTest()
	staleRequest.OptionalSyncTargets = []string{"hiveram.com"}
	staleRequest.Diagnosis.Problem = "Clawbot smoke lane investigation drifted to stale evidence."
	staleRequest.Diagnosis.EvidenceIDs = []string{"art_stale_log"}
	staleRequest.Diagnosis.Confidence = model.ConfidenceMedium
	seedSuccessfulPromotionWithWorkOrderSuffixForRetryTest(t, st, thread, staleRequest, WorkOrderRef{
		Project: "neurorouter-pro",
		ID:      999,
		Title:   "Resolve: Clawbot smoke lane is unstable",
	}, "stale")

	status, resp, raw := postPromoteForRetryTest(t, handler, thread.ThreadID, marshalJSON(t, request))
	if status != http.StatusOK {
		t.Fatalf("retry promote status = %d, body = %s", status, raw)
	}
	if resp.Status != "duplicate" {
		t.Fatalf("expected duplicate retry status, got %#v", resp)
	}
	if resp.Canonical.ID != 301 {
		t.Fatalf("expected retry to return the matching canonical WO, got %#v", resp.Canonical)
	}
	if len(bridge.drafts) != 0 {
		t.Fatalf("expected no workledger side effect on exact bound retry, got %d", len(bridge.drafts))
	}
}

func TestPromoteThreadRetrySuppressesFailedPendingWhenDanglingPendingExists(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	bridge := &fakeWorkOrderBridge{err: errors.New("bridge should not be called")}
	handler := NewHandlerWithOptions(
		st,
		openTestArtifactStore(t),
		keys,
		HandlerOptions{WorkOrders: bridge},
	)

	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	mustSeedThread(t, st, thread)

	request := validPromoteThreadRequestForRetryTest()
	seedSuccessfulPromotionForRetryTest(t, st, thread, request, WorkOrderRef{
		Project: "neurorouter-pro",
		ID:      401,
		Title:   "Resolve: Clawbot smoke lane is unstable",
	})
	recordDanglingPendingPromotionForRetryTest(t, st, thread.ThreadID, request)

	status, resp, raw := postPromoteForRetryTest(t, handler, thread.ThreadID, marshalJSON(t, request))
	if status != http.StatusOK {
		t.Fatalf("retry promote status = %d, body = %s", status, raw)
	}
	if resp.Status != "duplicate" {
		t.Fatalf("expected duplicate retry status, got %#v", resp)
	}
	if len(bridge.drafts) != 0 {
		t.Fatalf("expected no workledger side effect with dangling pending retry, got %d", len(bridge.drafts))
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.PendingPromotions) != 1 {
		t.Fatalf("expected duplicate retry not to accumulate pending records, got %#v", snapshot.PendingPromotions)
	}
	if snapshot.PendingPromotions[0].Status == model.PromotionStatusFailed {
		t.Fatalf("expected duplicate retry not to mark pending as failed, got %#v", snapshot.PendingPromotions[0])
	}
}

// WO-72: exact retry matching is stricter than thread/project/source-thread;
// stale evidence, sync targets, or diagnosis hashes must not satisfy it.
func TestPromotedWorkOrderMatchesRetryRejectsLooseReceiptBinding(t *testing.T) {
	t.Helper()

	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	request := validPromoteThreadRequestForRetryTest()
	draft, err := work.DraftFromThread(request.WorkledgerProject, thread, request.Diagnosis)
	if err != nil {
		t.Fatalf("DraftFromThread() error = %v", err)
	}

	at := time.Date(2026, 4, 18, 7, 0, 0, 0, time.UTC)
	diagnosisEnvelope, err := buildDiagnosisEnvelope(thread.ThreadID, request, at)
	if err != nil {
		t.Fatalf("buildDiagnosisEnvelope() error = %v", err)
	}
	workOrderEnvelope, err := buildWorkOrderEnvelope(thread.ThreadID, request, draft, WorkOrderRef{
		Project: "neurorouter-pro",
		ID:      501,
		Title:   "Resolve: Clawbot smoke lane is unstable",
	}, at.Add(time.Second))
	if err != nil {
		t.Fatalf("buildWorkOrderEnvelope() error = %v", err)
	}

	if _, ok := promotedWorkOrderMatchesRetry(workOrderEnvelope, diagnosisEnvelope, draft); !ok {
		t.Fatalf("expected exact work-order receipt to match retry")
	}

	mutatedEvidence := mutatePromotedRetryReceiptForTest(t, workOrderEnvelope, func(receipt *promotedRetryReceipt) {
		receipt.EvidenceIDs = []string{"art_other_log"}
	})
	if _, ok := promotedWorkOrderMatchesRetry(mutatedEvidence, diagnosisEnvelope, draft); ok {
		t.Fatalf("expected receipt with different evidence ids not to match retry")
	}

	mutatedConfidence := mutatePromotedRetryReceiptForTest(t, workOrderEnvelope, func(receipt *promotedRetryReceipt) {
		receipt.Confidence = model.ConfidenceMedium
	})
	if _, ok := promotedWorkOrderMatchesRetry(mutatedConfidence, diagnosisEnvelope, draft); ok {
		t.Fatalf("expected receipt with different confidence not to match retry")
	}

	mutatedSync := mutatePromotedRetryReceiptForTest(t, workOrderEnvelope, func(receipt *promotedRetryReceipt) {
		receipt.OptionalSyncTargets = []string{"hiveram.com"}
	})
	if _, ok := promotedWorkOrderMatchesRetry(mutatedSync, diagnosisEnvelope, draft); ok {
		t.Fatalf("expected receipt with different optional sync targets not to match retry")
	}

	mutatedDiagnosis := mutatePromotedRetryReceiptForTest(t, workOrderEnvelope, func(receipt *promotedRetryReceipt) {
		receipt.DiagnosisPayloadSHA256 = promotionPayloadSHA256([]byte(`{"different":true}`))
	})
	if _, ok := promotedWorkOrderMatchesRetry(mutatedDiagnosis, diagnosisEnvelope, draft); ok {
		t.Fatalf("expected receipt with different diagnosis payload hash not to match retry")
	}
}

func validPromoteThreadRequestForRetryTest() promoteThreadRequest {
	return promoteThreadRequest{
		RequestedBy:       "agent.investigator",
		WorkledgerProject: "neurorouter-pro",
		Diagnosis: model.Diagnosis{
			Problem:             "Clawbot smoke lane is unstable during release validation.",
			LikelyCause:         "The disposable ARM64 VM lost the current host handoff.",
			ProposedRemediation: []string{"Restore the current smoke VM identity."},
			EvidenceIDs:         []string{"art_smoke_log"},
			Confidence:          model.ConfidenceHigh,
			Verified:            true,
		},
	}
}

func postPromoteForRetryTest(
	t *testing.T,
	handler http.Handler,
	threadID string,
	reqBody []byte,
) (int, promoteThreadResponse, string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/v0/threads/"+threadID+"/promote", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp promoteThreadResponse
	if rec.Code >= http.StatusOK && rec.Code < http.StatusMultipleChoices {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Unmarshal(promote) error = %v", err)
		}
	}

	return rec.Code, resp, rec.Body.String()
}

func seedSuccessfulPromotionForRetryTest(
	t *testing.T,
	st *store.Store,
	thread model.Thread,
	request promoteThreadRequest,
	canonical WorkOrderRef,
) store.PromotionPendingRecord {
	t.Helper()

	return seedSuccessfulPromotionWithWorkOrderSuffixForRetryTest(t, st, thread, request, canonical, "")
}

func seedSuccessfulPromotionWithWorkOrderSuffixForRetryTest(
	t *testing.T,
	st *store.Store,
	thread model.Thread,
	request promoteThreadRequest,
	canonical WorkOrderRef,
	workOrderSuffix string,
) store.PromotionPendingRecord {
	t.Helper()

	return seedSuccessfulPromotionWithReceiptMutationForRetryTest(
		t,
		st,
		thread,
		request,
		canonical,
		workOrderSuffix,
		nil,
	)
}

func seedSuccessfulPromotionWithReceiptMutationForRetryTest(
	t *testing.T,
	st *store.Store,
	thread model.Thread,
	request promoteThreadRequest,
	canonical WorkOrderRef,
	workOrderSuffix string,
	mutate func(*promotedRetryReceipt),
) store.PromotionPendingRecord {
	t.Helper()

	at := time.Date(2026, 4, 18, 7, 0, 0, 0, time.UTC)
	draft, err := work.DraftFromThread(request.WorkledgerProject, thread, request.Diagnosis)
	if err != nil {
		t.Fatalf("DraftFromThread() error = %v", err)
	}

	diagnosisEnvelope, err := buildDiagnosisEnvelope(thread.ThreadID, request, at)
	if err != nil {
		t.Fatalf("buildDiagnosisEnvelope() error = %v", err)
	}
	pendingRecord := buildPendingPromotionRecord(diagnosisEnvelope, at)
	if err := st.RecordPromotionPending(t.Context(), pendingRecord); err != nil {
		t.Fatalf("RecordPromotionPending() error = %v", err)
	}

	workOrderEnvelope, err := buildWorkOrderEnvelope(thread.ThreadID, request, draft, canonical, at.Add(time.Second))
	if err != nil {
		t.Fatalf("buildWorkOrderEnvelope() error = %v", err)
	}
	if mutate != nil {
		workOrderEnvelope = mutatePromotedRetryReceiptForTest(t, workOrderEnvelope, mutate)
	}
	if workOrderSuffix != "" {
		workOrderEnvelope.MessageID += "_" + workOrderSuffix
		workOrderEnvelope.IdempotencyKey += "_" + workOrderSuffix
		workOrderEnvelope.Security.Nonce += "_" + workOrderSuffix
		workOrderEnvelope.Security.Signature += "_" + workOrderSuffix
	}
	if err := st.FinalizePromotion(
		t.Context(),
		pendingRecord.PendingMessageID,
		at.Add(2*time.Second),
		diagnosisEnvelope,
		workOrderEnvelope,
	); err != nil {
		t.Fatalf("FinalizePromotion() error = %v", err)
	}

	return pendingRecord
}

func mutatePromotedRetryReceiptForTest(
	t *testing.T,
	envelope model.Envelope,
	mutate func(*promotedRetryReceipt),
) model.Envelope {
	t.Helper()

	var receipt promotedRetryReceipt
	if err := json.Unmarshal(envelope.Payload, &receipt); err != nil {
		t.Fatalf("Unmarshal(receipt) error = %v", err)
	}
	mutate(&receipt)

	payload, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("Marshal(receipt) error = %v", err)
	}
	envelope.Payload = payload
	return envelope
}

// WO-77: bad legacy-lane fixtures need unique envelope identity without
// changing the promotion receipt payload under test.
func suffixPromotionEnvelopeForRetryTest(envelope model.Envelope, suffix string) model.Envelope {
	envelope.MessageID += "_" + suffix
	envelope.IdempotencyKey += "_" + suffix
	envelope.Security.Nonce += "_" + suffix
	envelope.Security.Signature += "_" + suffix
	return envelope
}

func recordDanglingPendingPromotionForRetryTest(
	t *testing.T,
	st *store.Store,
	threadID string,
	request promoteThreadRequest,
) {
	t.Helper()

	at := time.Date(2026, 4, 18, 7, 5, 0, 0, time.UTC)
	diagnosisEnvelope, err := buildDiagnosisEnvelope(threadID, request, at)
	if err != nil {
		t.Fatalf("buildDiagnosisEnvelope() error = %v", err)
	}

	record := buildPendingPromotionRecord(diagnosisEnvelope, at)
	record.PendingMessageID = "pending_retry_" + diagnosisEnvelope.MessageID
	if err := st.RecordPromotionPending(t.Context(), record); err != nil {
		t.Fatalf("RecordPromotionPending() error = %v", err)
	}
}
