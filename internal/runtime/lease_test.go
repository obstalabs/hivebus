package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLeaseRenewAndCompleteOverHTTP(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	handler := NewHandler(st, nil)

	thread := sampleThread()
	mustCreateThread(t, handler, thread)

	task := sampleWorkerTask(thread.ThreadID, "msg_task", "idem_task", "worker.smokevm")
	mustAppendMessage(t, handler, thread.ThreadID, task, http.StatusAccepted)

	accepted := sampleAcceptedEnvelope(task, "worker.smokevm", "msg_accepted", "idem_accepted", task.SentAt.Add(1*time.Minute))
	claimBody := marshalJSON(t, workerClaimRequest{
		WorkerID:         "worker.smokevm",
		TaskMessageID:    task.MessageID,
		AcceptedEnvelope: accepted,
		LeaseSeconds:     120,
	})
	claimReq := httptest.NewRequest(http.MethodPost, "/v0/workers/claim", bytes.NewReader(claimBody))
	claimReq.Header.Set("Content-Type", "application/json")
	claimRec := httptest.NewRecorder()
	handler.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusAccepted {
		t.Fatalf("claim status = %d, body = %s", claimRec.Code, claimRec.Body.String())
	}

	var claimResponse workerClaimResponse
	if err := json.Unmarshal(claimRec.Body.Bytes(), &claimResponse); err != nil {
		t.Fatalf("Unmarshal(claim) error = %v", err)
	}

	renewBody := marshalJSON(t, leaseRenewRequest{
		WorkerID:     "worker.smokevm",
		LeaseSeconds: 300,
	})
	renewReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/workers/leases/"+claimResponse.Lease.LeaseID+"/renew",
		bytes.NewReader(renewBody),
	)
	renewReq.Header.Set("Content-Type", "application/json")
	renewRec := httptest.NewRecorder()
	handler.ServeHTTP(renewRec, renewReq)
	if renewRec.Code != http.StatusOK {
		t.Fatalf("renew status = %d, body = %s", renewRec.Code, renewRec.Body.String())
	}

	result := sampleResultEnvelope(task, "worker.smokevm", "msg_result", "idem_result", task.SentAt.Add(2*time.Minute))
	completeBody := marshalJSON(t, leaseCompleteRequest{
		WorkerID:       "worker.smokevm",
		ResultEnvelope: result,
	})
	completeReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/workers/leases/"+claimResponse.Lease.LeaseID+"/complete",
		bytes.NewReader(completeBody),
	)
	completeReq.Header.Set("Content-Type", "application/json")
	completeRec := httptest.NewRecorder()
	handler.ServeHTTP(completeRec, completeReq)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", completeRec.Code, completeRec.Body.String())
	}

	threadReq := httptest.NewRequest(http.MethodGet, "/v0/threads/"+thread.ThreadID, nil)
	threadRec := httptest.NewRecorder()
	handler.ServeHTTP(threadRec, threadReq)
	if threadRec.Code != http.StatusOK {
		t.Fatalf("thread fetch status = %d, body = %s", threadRec.Code, threadRec.Body.String())
	}

	var snapshot struct {
		LeaseReceipts []map[string]any `json:"lease_receipts"`
	}
	if err := json.Unmarshal(threadRec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("Unmarshal(snapshot) error = %v", err)
	}
	if len(snapshot.LeaseReceipts) != 3 {
		t.Fatalf("expected 3 lease receipts, got %d", len(snapshot.LeaseReceipts))
	}
}

func TestLeaseCompleteRejectsDuplicateCompletion(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	handler := NewHandler(st, nil)

	thread := sampleThread()
	mustCreateThread(t, handler, thread)

	task := sampleWorkerTask(thread.ThreadID, "msg_task", "idem_task", "worker.smokevm")
	mustAppendMessage(t, handler, thread.ThreadID, task, http.StatusAccepted)

	accepted := sampleAcceptedEnvelope(task, "worker.smokevm", "msg_accepted", "idem_accepted", task.SentAt.Add(1*time.Minute))
	claimBody := marshalJSON(t, workerClaimRequest{
		WorkerID:         "worker.smokevm",
		TaskMessageID:    task.MessageID,
		AcceptedEnvelope: accepted,
		LeaseSeconds:     120,
	})
	claimReq := httptest.NewRequest(http.MethodPost, "/v0/workers/claim", bytes.NewReader(claimBody))
	claimReq.Header.Set("Content-Type", "application/json")
	claimRec := httptest.NewRecorder()
	handler.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusAccepted {
		t.Fatalf("claim status = %d, body = %s", claimRec.Code, claimRec.Body.String())
	}

	var claimResponse workerClaimResponse
	if err := json.Unmarshal(claimRec.Body.Bytes(), &claimResponse); err != nil {
		t.Fatalf("Unmarshal(claim) error = %v", err)
	}

	result := sampleResultEnvelope(task, "worker.smokevm", "msg_result", "idem_result", task.SentAt.Add(2*time.Minute))
	completeBody := marshalJSON(t, leaseCompleteRequest{
		WorkerID:       "worker.smokevm",
		ResultEnvelope: result,
	})
	completePath := "/v0/workers/leases/" + claimResponse.Lease.LeaseID + "/complete"

	firstCompleteReq := httptest.NewRequest(http.MethodPost, completePath, bytes.NewReader(completeBody))
	firstCompleteReq.Header.Set("Content-Type", "application/json")
	firstCompleteRec := httptest.NewRecorder()
	handler.ServeHTTP(firstCompleteRec, firstCompleteReq)
	if firstCompleteRec.Code != http.StatusOK {
		t.Fatalf("first complete status = %d, body = %s", firstCompleteRec.Code, firstCompleteRec.Body.String())
	}

	secondCompleteReq := httptest.NewRequest(http.MethodPost, completePath, bytes.NewReader(completeBody))
	secondCompleteReq.Header.Set("Content-Type", "application/json")
	secondCompleteRec := httptest.NewRecorder()
	handler.ServeHTTP(secondCompleteRec, secondCompleteReq)
	if secondCompleteRec.Code != http.StatusConflict {
		t.Fatalf("second complete status = %d, body = %s", secondCompleteRec.Code, secondCompleteRec.Body.String())
	}
}
