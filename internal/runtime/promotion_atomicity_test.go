package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ppiankov/hivebus/internal/model"
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
