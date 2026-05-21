package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/work"
)

type fakeWorkOrderBridge struct {
	drafts []work.Draft
	ref    WorkOrderRef
	err    error
}

func (f *fakeWorkOrderBridge) CreateWorkOrder(_ context.Context, draft work.Draft) (WorkOrderRef, error) {
	f.drafts = append(f.drafts, draft)
	if f.err != nil {
		return WorkOrderRef{}, f.err
	}
	return f.ref, nil
}

type fakeSyncHook struct {
	receipts []SyncReceipt
}

func (f *fakeSyncHook) SyncWorkOrder(_ context.Context, _ work.Draft, _ WorkOrderRef) (SyncReceipt, error) {
	receipt := SyncReceipt{
		Target: "hiveram.com",
		Status: "queued",
		Detail: "optional sync hook accepted the work order",
	}
	f.receipts = append(f.receipts, receipt)
	return receipt, nil
}

func TestNullbotIntakeCreatesThreadAndSupportsClarificationFlow(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	thread := model.Thread{
		ThreadID:     "thr_nullbot_intake",
		Title:        "Nullbot intake for smoke lane",
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
			{
				ID:          "worker.smokevm",
				Type:        model.ParticipantTypeAgent,
				Kind:        model.ParticipantAgent,
				DisplayName: "Smoke Worker",
				Visibility:  model.ParticipantVisibilityThread,
				Agent: &model.AgentParticipant{
					AgentID: "worker.smokevm",
				},
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	artifact := model.Artifact{
		ArtifactID:  "art_smoke_log",
		Name:        "smoke.log",
		Kind:        "log",
		URI:         "artifact://sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SizeBytes:   128,
		ContentType: "text/plain",
	}

	initialTask := model.Envelope{
		MessageID:      "msg_nullbot_intake",
		ThreadID:       thread.ThreadID,
		From:           "collector.nullbot",
		To:             []string{"worker.smokevm"},
		Type:           model.MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"collect smoke logs from vm"}`),
		ArtifactIDs:    []string{artifact.ArtifactID},
		SentAt:         now.Add(1 * time.Minute),
		IdempotencyKey: "idem_nullbot_intake",
		Trace: model.Trace{
			CorrelationID: thread.ThreadID,
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_nullbot_intake",
		},
	}

	body := marshalJSON(t, nullbotIntakeRequest{
		Thread:      thread,
		Artifacts:   []model.Artifact{artifact},
		InitialTask: initialTask,
	})
	req := httptest.NewRequest(http.MethodPost, "/v0/intake/nullbot", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v0/intake/nullbot status = %d, body = %s", rec.Code, rec.Body.String())
	}

	pollBody := marshalJSON(t, workerPollRequest{WorkerID: "worker.smokevm"})
	pollReq := httptest.NewRequest(http.MethodPost, "/v0/workers/poll", bytes.NewReader(pollBody))
	pollReq.Header.Set("Content-Type", "application/json")
	pollReq.Header.Set("Authorization", "Bearer worker-secret")
	pollRec := httptest.NewRecorder()
	handler.ServeHTTP(pollRec, pollReq)
	if pollRec.Code != http.StatusOK {
		t.Fatalf("POST /v0/workers/poll status = %d, body = %s", pollRec.Code, pollRec.Body.String())
	}

	var pollResp workerPollResponse
	if err := json.Unmarshal(pollRec.Body.Bytes(), &pollResp); err != nil {
		t.Fatalf("Unmarshal(poll) error = %v", err)
	}
	if pollResp.Task == nil {
		t.Fatalf("expected available task, got %#v", pollResp)
	}

	claimAccepted := model.Envelope{
		MessageID:      "msg_nullbot_accept",
		ThreadID:       thread.ThreadID,
		From:           "worker.smokevm",
		To:             []string{"collector.nullbot"},
		Type:           model.MessageTypeTaskAccepted,
		Payload:        json.RawMessage(`{"status":"accepted"}`),
		ReplyTo:        initialTask.MessageID,
		SentAt:         now.Add(90 * time.Second),
		IdempotencyKey: "idem_nullbot_accept",
		Trace: model.Trace{
			CorrelationID: thread.ThreadID,
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_nullbot_accept",
		},
	}
	claimBody := marshalJSON(t, workerClaimRequest{
		WorkerID:         "worker.smokevm",
		TaskMessageID:    pollResp.Task.Envelope.MessageID,
		AcceptedEnvelope: claimAccepted,
	})
	claimReq := httptest.NewRequest(http.MethodPost, "/v0/workers/claim", bytes.NewReader(claimBody))
	claimReq.Header.Set("Content-Type", "application/json")
	claimReq.Header.Set("Authorization", "Bearer worker-secret")
	claimRec := httptest.NewRecorder()
	handler.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusAccepted {
		t.Fatalf("POST /v0/workers/claim status = %d, body = %s", claimRec.Code, claimRec.Body.String())
	}

	var claimResp workerClaimResponse
	if err := json.Unmarshal(claimRec.Body.Bytes(), &claimResp); err != nil {
		t.Fatalf("Unmarshal(claim) error = %v", err)
	}

	deadline := now.Add(10 * time.Minute)
	clarificationBody := marshalJSON(t, clarificationRequestPayload{
		WorkerID: "worker.smokevm",
		Request: model.Envelope{
			MessageID:      "msg_nullbot_clarify",
			ThreadID:       thread.ThreadID,
			From:           "worker.smokevm",
			To:             []string{"collector.nullbot"},
			Type:           model.MessageTypeClarifyRequest,
			Payload:        json.RawMessage(`{"question":"which release candidate should I test?"}`),
			ReplyTo:        initialTask.MessageID,
			Deadline:       &deadline,
			SentAt:         now.Add(2 * time.Minute),
			IdempotencyKey: "idem_nullbot_clarify",
			Trace: model.Trace{
				CorrelationID: thread.ThreadID,
				Verified:      true,
			},
			Security: model.Security{
				Scheme: "ed25519",
				Nonce:  "nonce_nullbot_clarify",
			},
		},
	})
	clarifyReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/workers/leases/"+claimResp.Lease.LeaseID+"/clarifications/request",
		bytes.NewReader(clarificationBody),
	)
	clarifyReq.Header.Set("Content-Type", "application/json")
	clarifyReq.Header.Set("Authorization", "Bearer worker-secret")
	clarifyRec := httptest.NewRecorder()
	handler.ServeHTTP(clarifyRec, clarifyReq)
	if clarifyRec.Code != http.StatusAccepted {
		t.Fatalf("POST clarification request status = %d, body = %s", clarifyRec.Code, clarifyRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v0/threads/"+thread.ThreadID, nil)
	getReq.Header.Set("Authorization", "Bearer operator-secret")
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /v0/threads/{id} status = %d, body = %s", getRec.Code, getRec.Body.String())
	}

	var snapshot struct {
		Thread    model.Thread     `json:"thread"`
		Envelopes []model.Envelope `json:"envelopes"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("Unmarshal(snapshot) error = %v", err)
	}
	if len(snapshot.Envelopes) != 3 {
		t.Fatalf("expected 3 envelopes after clarification, got %d", len(snapshot.Envelopes))
	}
	if snapshot.Envelopes[2].Type != model.MessageTypeClarifyRequest {
		t.Fatalf("expected clarification.request, got %s", snapshot.Envelopes[2].Type)
	}
}

func TestPromoteThreadCreatesCanonicalWorkOrderAndOptionalSync(t *testing.T) {
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
	syncHook := &fakeSyncHook{}
	handler := NewHandlerWithOptions(
		st,
		openTestArtifactStore(t),
		keys,
		HandlerOptions{
			WorkOrders: bridge,
			SyncHooks: map[string]ExecutionSyncHook{
				"hiveram.com": syncHook,
			},
		},
	)

	thread := sampleThread()
	thread.Status = model.ThreadStatusInvestigating
	mustSeedThread(t, st, thread)

	reqBody := marshalJSON(t, promoteThreadRequest{
		RequestedBy:       "agent.investigator",
		WorkledgerProject: "neurorouter-pro",
		Diagnosis: model.Diagnosis{
			Problem:             "Clawbot smoke lane is unstable during release validation.",
			LikelyCause:         "The disposable ARM64 VM loses context between runs and needs clearer task continuity.",
			ProposedRemediation: []string{"Persist the next-step context in the thread.", "Re-run the smoke lane after restoring the release state."},
			EvidenceIDs:         []string{"art_smoke_log"},
			Confidence:          model.ConfidenceHigh,
			Verified:            true,
		},
		OptionalSyncTargets: []string{"hiveram.com"},
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

	if resp.Canonical.ID != 201 {
		t.Fatalf("expected work order id 201, got %#v", resp.Canonical)
	}
	if len(bridge.drafts) != 1 {
		t.Fatalf("expected one workledger draft, got %d", len(bridge.drafts))
	}
	if len(resp.Sync) != 1 || resp.Sync[0].Status != "queued" {
		t.Fatalf("expected queued sync receipt, got %#v", resp.Sync)
	}
	if resp.Snapshot.Thread.Status != model.ThreadStatusReadyForWork {
		t.Fatalf("expected promoted thread status %q, got %q", model.ThreadStatusReadyForWork, resp.Snapshot.Thread.Status)
	}
	if len(resp.Snapshot.Envelopes) != 2 {
		t.Fatalf("expected diagnosis + work order envelopes, got %d", len(resp.Snapshot.Envelopes))
	}
	if resp.Snapshot.Envelopes[0].Type != model.MessageTypeDiagnosisPropose {
		t.Fatalf("expected first envelope diagnosis.proposed, got %s", resp.Snapshot.Envelopes[0].Type)
	}
	if resp.Snapshot.Envelopes[1].Type != model.MessageTypeWorkOrderCreate {
		t.Fatalf("expected second envelope work_order.create, got %s", resp.Snapshot.Envelopes[1].Type)
	}

	recoveryReq := httptest.NewRequest(http.MethodGet, "/v0/threads/"+thread.ThreadID+"/recovery", nil)
	recoveryReq.Header.Set("Authorization", "Bearer operator-secret")
	recoveryRec := httptest.NewRecorder()
	handler.ServeHTTP(recoveryRec, recoveryReq)
	if recoveryRec.Code != http.StatusOK {
		t.Fatalf("GET /v0/threads/{id}/recovery status = %d, body = %s", recoveryRec.Code, recoveryRec.Body.String())
	}

	var capsule model.PromotedThreadRecoveryCapsule
	if err := json.Unmarshal(recoveryRec.Body.Bytes(), &capsule); err != nil {
		t.Fatalf("Unmarshal(recovery) error = %v", err)
	}
	if capsule.Type != model.RecoveryCapsuleTypePromotedThread {
		t.Fatalf("expected promoted recovery capsule, got %q", capsule.Type)
	}
	if capsule.Promotion.WorkOrderID != 201 {
		t.Fatalf("expected recovery capsule to reference WO-201, got %#v", capsule.Promotion)
	}
	if !capsule.VerifiedDiagnosis.Verified {
		t.Fatalf("expected verified diagnosis in recovery capsule, got %#v", capsule.VerifiedDiagnosis)
	}
	if len(capsule.VerifiedFacts) == 0 {
		t.Fatal("expected recovery capsule verified facts")
	}
}

func TestAppendEnvelopeRejectsVerifiedPromotionEnvelopeWithoutPromotionStatus(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	thread := sampleThread()
	mustSeedThread(t, st, thread)

	diagnosis := model.Diagnosis{
		Problem:             "Smoke lane blocked.",
		LikelyCause:         "Direct append should not mint fresh legacy promotion truth.",
		ProposedRemediation: []string{"Require explicit promotion status or use /promote."},
		EvidenceIDs:         []string{"art_smoke_log"},
		Confidence:          model.ConfidenceHigh,
		Verified:            true,
	}
	body := marshalJSON(t, model.Envelope{
		MessageID:      "msg_direct_append_diagnosis",
		ThreadID:       thread.ThreadID,
		From:           "agent.investigator",
		To:             []string{"service.hivebus"},
		Type:           model.MessageTypeDiagnosisPropose,
		Payload:        marshalJSON(t, diagnosis),
		SentAt:         thread.CreatedAt.Add(time.Minute),
		IdempotencyKey: "idem_direct_append_diagnosis",
		Trace: model.Trace{
			CorrelationID: thread.ThreadID,
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_direct_append_diagnosis",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v0/threads/"+thread.ThreadID+"/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /v0/threads/{id}/messages status = %d, body = %s", rec.Code, rec.Body.String())
	}

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if len(snapshot.Envelopes) != 0 {
		t.Fatalf("expected no appended promotion envelope after rejection, got %#v", snapshot.Envelopes)
	}
}
