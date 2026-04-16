package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDispatchCreatesClaimableTaskForTargetWorker(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, keys)

	dispatchBody := marshalJSON(t, dispatchRequest{
		Title:  "Smoke test neurorouter release",
		Task:   "Run the smoke VM release test on the new build.",
		Target: "worker.smokevm",
		Sender: dispatchSender{
			ID:        "codex.operator",
			SessionID: "sess_123",
		},
		ContextSnapshot: &dispatchContextSnapshot{
			CWD:               "/workspace/neurorouter-pro",
			Repo:              "obstalabs/neurorouter-pro",
			GitBranch:         "main",
			GitHead:           "abc123",
			WorkledgerProject: "neurorouter-pro",
			RelevantPaths:     []string{"cmd/neurorouter", "internal/neurorouter"},
			TestCommands:      []string{"go test ./internal/neurorouter ./cmd/neurorouter"},
		},
	})
	dispatchReq := httptest.NewRequest(http.MethodPost, "/v0/dispatch", bytes.NewReader(dispatchBody))
	dispatchReq.Header.Set("Content-Type", "application/json")
	dispatchReq.Header.Set("Authorization", "Bearer operator-secret")
	dispatchRec := httptest.NewRecorder()
	handler.ServeHTTP(dispatchRec, dispatchReq)
	if dispatchRec.Code != http.StatusCreated {
		t.Fatalf("POST /v0/dispatch status = %d, body = %s", dispatchRec.Code, dispatchRec.Body.String())
	}

	var dispatchResp dispatchResponse
	if err := json.Unmarshal(dispatchRec.Body.Bytes(), &dispatchResp); err != nil {
		t.Fatalf("Unmarshal(dispatch) error = %v", err)
	}
	if dispatchResp.Status != "queued" {
		t.Fatalf("unexpected dispatch response %#v", dispatchResp)
	}
	if len(dispatchResp.Snapshot.Envelopes) != 1 {
		t.Fatalf("expected 1 dispatch envelope, got %d", len(dispatchResp.Snapshot.Envelopes))
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
	if pollResp.Status != "available" {
		t.Fatalf("unexpected poll response %#v", pollResp)
	}
	if pollResp.Task == nil || pollResp.Task.Envelope.MessageID != dispatchResp.TaskMessageID {
		t.Fatalf("expected dispatched task %q, got %#v", dispatchResp.TaskMessageID, pollResp.Task)
	}
}

func TestDispatchCreatesClaimableTaskForCapabilityWorker(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, keys)

	dispatchBody := marshalJSON(t, dispatchRequest{
		Title:      "Review runtime auth",
		Task:       "Inspect the auth handlers and summarize edge cases.",
		Capability: "code.review",
		Sender: dispatchSender{
			ID:        "claude.operator",
			SessionID: "sess_review",
		},
	})
	dispatchReq := httptest.NewRequest(http.MethodPost, "/v0/dispatch", bytes.NewReader(dispatchBody))
	dispatchReq.Header.Set("Content-Type", "application/json")
	dispatchReq.Header.Set("Authorization", "Bearer operator-secret")
	dispatchRec := httptest.NewRecorder()
	handler.ServeHTTP(dispatchRec, dispatchReq)
	if dispatchRec.Code != http.StatusCreated {
		t.Fatalf("POST /v0/dispatch status = %d, body = %s", dispatchRec.Code, dispatchRec.Body.String())
	}

	pollBody := marshalJSON(t, workerPollRequest{
		WorkerID:     "worker.reviewer",
		Capabilities: []string{"code.review"},
	})
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
	if pollResp.Status != "available" || pollResp.Task == nil {
		t.Fatalf("unexpected poll response %#v", pollResp)
	}
	if pollResp.Task.Envelope.Capability != "code.review" {
		t.Fatalf("expected capability task, got %#v", pollResp.Task.Envelope)
	}
}

func TestDispatchRejectsAmbiguousTargeting(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, keys)

	dispatchBody := marshalJSON(t, dispatchRequest{
		Title:      "Bad dispatch",
		Task:       "This should fail.",
		Target:     "worker.smokevm",
		Capability: "code.review",
		Sender: dispatchSender{
			ID:        "codex.operator",
			SessionID: "sess_bad",
		},
	})
	dispatchReq := httptest.NewRequest(http.MethodPost, "/v0/dispatch", bytes.NewReader(dispatchBody))
	dispatchReq.Header.Set("Content-Type", "application/json")
	dispatchReq.Header.Set("Authorization", "Bearer operator-secret")
	dispatchRec := httptest.NewRecorder()
	handler.ServeHTTP(dispatchRec, dispatchReq)
	if dispatchRec.Code != http.StatusBadRequest {
		t.Fatalf("POST /v0/dispatch status = %d, body = %s", dispatchRec.Code, dispatchRec.Body.String())
	}
}

func TestDispatchResumeCapsuleForQueuedTask(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, keys)

	threadID := mustDispatchTask(t, handler, dispatchRequest{
		Title:  "Re-run smoke lane",
		Task:   "Re-run the disposable VM smoke lane after the release fix.",
		Target: "worker.smokevm",
		Sender: dispatchSender{
			ID:        "claude.operator",
			SessionID: "sess_resume",
		},
		ContextSnapshot: &dispatchContextSnapshot{
			Repo:              "obstalabs/neurorouter-pro",
			WorkledgerProject: "neurorouter-pro",
			GitBranch:         "main",
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v0/dispatch/"+threadID+"/resume", nil)
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v0/dispatch/{id}/resume status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var capsule resumeCapsule
	if err := json.Unmarshal(rec.Body.Bytes(), &capsule); err != nil {
		t.Fatalf("Unmarshal(capsule) error = %v", err)
	}
	if capsule.State != "queued" {
		t.Fatalf("expected queued state, got %#v", capsule)
	}
	if capsule.TargetAlias != "worker.smokevm" {
		t.Fatalf("expected target alias worker.smokevm, got %#v", capsule)
	}
	if capsule.Project != "neurorouter-pro" || capsule.Repo != "obstalabs/neurorouter-pro" {
		t.Fatalf("expected project and repo context, got %#v", capsule)
	}
	if capsule.SenderSessionID != "sess_resume" {
		t.Fatalf("expected sender session id, got %#v", capsule)
	}
}

func TestDispatchResumeCapsuleForCompletedTask(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, keys)

	threadID, taskMessageID := mustDispatchTaskWithMessageID(t, handler, dispatchRequest{
		Title:      "Review auth handlers",
		Task:       "Review the auth handlers and report any edge cases.",
		Capability: "code.review",
		Sender: dispatchSender{
			ID:        "codex.operator",
			SessionID: "sess_complete",
		},
		ContextSnapshot: &dispatchContextSnapshot{
			Repo:              "obstalabs/hivebus",
			WorkledgerProject: "hivebus",
			GitBranch:         "main",
		},
	})

	pollBody := marshalJSON(t, workerPollRequest{
		WorkerID:     "worker.reviewer",
		Capabilities: []string{"code.review"},
	})
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
		t.Fatalf("expected dispatched task, got %#v", pollResp)
	}

	accepted := sampleAcceptedEnvelope(
		pollResp.Task.Envelope,
		"worker.reviewer",
		"msg_accepted_resume",
		"idem_accepted_resume",
		pollResp.Task.Envelope.SentAt.Add(1*time.Minute),
	)
	claimBody := marshalJSON(t, workerClaimRequest{
		WorkerID:         "worker.reviewer",
		TaskMessageID:    taskMessageID,
		AcceptedEnvelope: accepted,
		LeaseSeconds:     300,
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

	result := sampleResultEnvelope(
		pollResp.Task.Envelope,
		"worker.reviewer",
		"msg_result_resume",
		"idem_result_resume",
		pollResp.Task.Envelope.SentAt.Add(2*time.Minute),
	)
	completeBody := marshalJSON(t, leaseCompleteRequest{
		WorkerID:       "worker.reviewer",
		ResultEnvelope: result,
	})
	completeReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/workers/leases/"+claimResp.Lease.LeaseID+"/complete",
		bytes.NewReader(completeBody),
	)
	completeReq.Header.Set("Content-Type", "application/json")
	completeReq.Header.Set("Authorization", "Bearer worker-secret")
	completeRec := httptest.NewRecorder()
	handler.ServeHTTP(completeRec, completeReq)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("POST /v0/workers/leases/{id}/complete status = %d, body = %s", completeRec.Code, completeRec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/v0/dispatch/"+threadID+"/resume", nil)
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v0/dispatch/{id}/resume status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var capsule resumeCapsule
	if err := json.Unmarshal(rec.Body.Bytes(), &capsule); err != nil {
		t.Fatalf("Unmarshal(capsule) error = %v", err)
	}
	if capsule.State != "completed" {
		t.Fatalf("expected completed state, got %#v", capsule)
	}
	if capsule.NextStep == "" || capsule.LastEventAt == "" {
		t.Fatalf("expected completion guidance, got %#v", capsule)
	}
}

func mustDispatchTask(t *testing.T, handler http.Handler, request dispatchRequest) string {
	t.Helper()

	threadID, _ := mustDispatchTaskWithMessageID(t, handler, request)
	return threadID
}

func mustDispatchTaskWithMessageID(t *testing.T, handler http.Handler, request dispatchRequest) (string, string) {
	t.Helper()

	body := marshalJSON(t, request)
	req := httptest.NewRequest(http.MethodPost, "/v0/dispatch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v0/dispatch status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var response dispatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal(dispatch) error = %v", err)
	}

	return response.ThreadID, response.TaskMessageID
}
