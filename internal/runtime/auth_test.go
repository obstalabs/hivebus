package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthAllowsWorkerOnWorkerEndpointsOnly(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, keys)

	pollBody := marshalJSON(t, workerPollRequest{WorkerID: "worker.smokevm"})
	pollReq := httptest.NewRequest(http.MethodPost, "/v0/workers/poll", bytes.NewReader(pollBody))
	pollReq.Header.Set("Content-Type", "application/json")
	pollReq.Header.Set("Authorization", "Bearer worker-secret")
	pollRec := httptest.NewRecorder()
	handler.ServeHTTP(pollRec, pollReq)
	if pollRec.Code != http.StatusOK {
		t.Fatalf("worker poll status = %d, body = %s", pollRec.Code, pollRec.Body.String())
	}

	threadReq := httptest.NewRequest(http.MethodGet, "/v0/threads/thr_123", nil)
	threadReq.Header.Set("Authorization", "Bearer worker-secret")
	threadRec := httptest.NewRecorder()
	handler.ServeHTTP(threadRec, threadReq)
	if threadRec.Code != http.StatusForbidden {
		t.Fatalf("worker thread fetch status = %d, body = %s", threadRec.Code, threadRec.Body.String())
	}
}

func TestAuthRequiresValidBearerToken(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, keys)

	req := httptest.NewRequest(http.MethodGet, "/v0/threads/thr_123", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestOperatorCanAccessThreadEndpoints(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, keys)

	thread := sampleThread()
	body := marshalJSON(t, thread)
	req := httptest.NewRequest(http.MethodPost, "/v0/threads", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("operator create thread status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func mustTestKeyStore(t *testing.T) *KeyStore {
	t.Helper()

	entries := []TokenEntry{
		{ID: "worker", KeyHash: HashToken("worker-secret"), Role: RoleWorker},
		{ID: "operator", KeyHash: HashToken("operator-secret"), Role: RoleOperator},
	}

	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("Marshal(entries) error = %v", err)
	}

	keys, err := ParseKeyStore(data)
	if err != nil {
		t.Fatalf("ParseKeyStore() error = %v", err)
	}

	return keys
}
