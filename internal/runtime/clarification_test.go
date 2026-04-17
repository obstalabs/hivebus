package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

func TestClarificationRequestResponseFlowBlocksCompletionOverHTTP(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	thread := sampleThread()
	mustCreateThreadWithAuth(t, handler, thread)

	task := sampleWorkerTask(thread.ThreadID, "msg_task", "idem_task", "worker.smokevm")
	now := currentTime()
	task.SentAt = now.Add(-3 * time.Minute)
	mustAppendMessageWithAuth(t, handler, thread.ThreadID, task, http.StatusAccepted)

	accepted := sampleAcceptedEnvelope(task, "worker.smokevm", "msg_accepted", "idem_accepted", task.SentAt.Add(1*time.Minute))
	claimBody := marshalJSON(t, workerClaimRequest{
		WorkerID:         "worker.smokevm",
		TaskMessageID:    task.MessageID,
		AcceptedEnvelope: accepted,
		LeaseSeconds:     300,
	})
	claimReq := httptest.NewRequest(http.MethodPost, "/v0/workers/claim", bytes.NewReader(claimBody))
	claimReq.Header.Set("Content-Type", "application/json")
	claimReq.Header.Set("Authorization", "Bearer worker-secret")
	claimRec := httptest.NewRecorder()
	handler.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusAccepted {
		t.Fatalf("claim status = %d, body = %s", claimRec.Code, claimRec.Body.String())
	}

	var claimResp workerClaimResponse
	if err := json.Unmarshal(claimRec.Body.Bytes(), &claimResp); err != nil {
		t.Fatalf("Unmarshal(claim) error = %v", err)
	}

	deadline := now.Add(5 * time.Minute)
	clarification := model.Envelope{
		MessageID:      "msg_clarify",
		ThreadID:       thread.ThreadID,
		From:           "worker.smokevm",
		To:             []string{task.From},
		Type:           model.MessageTypeClarifyRequest,
		Payload:        []byte(`{"question":"which release?"}`),
		ReplyTo:        task.MessageID,
		Deadline:       &deadline,
		SentAt:         now.Add(-2 * time.Minute),
		IdempotencyKey: "idem_clarify",
		Trace: model.Trace{
			CorrelationID: task.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_clarify",
		},
	}
	requestBody := marshalJSON(t, clarificationRequestPayload{
		WorkerID: "worker.smokevm",
		Request:  clarification,
	})
	requestReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/workers/leases/"+claimResp.Lease.LeaseID+"/clarifications/request",
		bytes.NewReader(requestBody),
	)
	requestReq.Header.Set("Content-Type", "application/json")
	requestReq.Header.Set("Authorization", "Bearer worker-secret")
	requestRec := httptest.NewRecorder()
	handler.ServeHTTP(requestRec, requestReq)
	if requestRec.Code != http.StatusAccepted {
		t.Fatalf("clarification request status = %d, body = %s", requestRec.Code, requestRec.Body.String())
	}

	result := sampleResultEnvelope(task, "worker.smokevm", "msg_result", "idem_result", task.SentAt.Add(3*time.Minute))
	completeBody := marshalJSON(t, leaseCompleteRequest{
		WorkerID:       "worker.smokevm",
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
	if completeRec.Code != http.StatusConflict {
		t.Fatalf("expected conflict before clarification response, got %d body=%s", completeRec.Code, completeRec.Body.String())
	}

	response := model.Envelope{
		MessageID:      "msg_answer",
		ThreadID:       thread.ThreadID,
		From:           task.From,
		To:             []string{"worker.smokevm"},
		Type:           model.MessageTypeClarifyResponse,
		Payload:        []byte(`{"answer":"v0.17.2"}`),
		ReplyTo:        clarification.MessageID,
		SentAt:         now.Add(-1 * time.Minute),
		IdempotencyKey: "idem_answer",
		Trace: model.Trace{
			CorrelationID: task.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_answer",
		},
	}
	responseBody := marshalJSON(t, clarificationResponsePayload{Response: response})
	responseReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/threads/"+thread.ThreadID+"/clarifications/response",
		bytes.NewReader(responseBody),
	)
	responseReq.Header.Set("Content-Type", "application/json")
	responseReq.Header.Set("Authorization", "Bearer operator-secret")
	responseRec := httptest.NewRecorder()
	handler.ServeHTTP(responseRec, responseReq)
	if responseRec.Code != http.StatusAccepted {
		t.Fatalf("clarification response status = %d, body = %s", responseRec.Code, responseRec.Body.String())
	}

	completeReq2 := httptest.NewRequest(
		http.MethodPost,
		"/v0/workers/leases/"+claimResp.Lease.LeaseID+"/complete",
		bytes.NewReader(completeBody),
	)
	completeReq2.Header.Set("Content-Type", "application/json")
	completeReq2.Header.Set("Authorization", "Bearer worker-secret")
	completeRec2 := httptest.NewRecorder()
	handler.ServeHTTP(completeRec2, completeReq2)
	if completeRec2.Code != http.StatusOK {
		t.Fatalf("expected completion after clarification response, got %d body=%s", completeRec2.Code, completeRec2.Body.String())
	}
}

func mustAppendMessageWithAuth(
	t *testing.T,
	handler http.Handler,
	threadID string,
	envelope any,
	expectedStatus int,
) {
	t.Helper()

	body := marshalJSON(t, envelope)
	req := httptest.NewRequest(http.MethodPost, "/v0/threads/"+threadID+"/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != expectedStatus {
		t.Fatalf("POST /v0/threads/%s/messages status = %d, body = %s", threadID, rec.Code, rec.Body.String())
	}
}
