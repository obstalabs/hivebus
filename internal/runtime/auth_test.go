package runtime

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthAllowsWorkerOnWorkerEndpointsOnly(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

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

func TestAuthAllowsSignedOperatorToken(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys, privateKey := mustSignedTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	thread := sampleThread()
	body := marshalJSON(t, thread)
	req := httptest.NewRequest(http.MethodPost, "/v0/threads", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mustSignedToken(t, privateKey, SignedTokenClaims{
		Subject:   "operator.sig",
		Role:      RoleOperator,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("signed operator create thread status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuthRequiresValidBearerToken(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	req := httptest.NewRequest(http.MethodGet, "/v0/threads/thr_123", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuthRejectsExpiredSignedToken(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys, privateKey := mustSignedTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	req := httptest.NewRequest(http.MethodGet, "/v0/threads/thr_123", nil)
	req.Header.Set("Authorization", "Bearer "+mustSignedToken(t, privateKey, SignedTokenClaims{
		Subject:   "operator.expired",
		Role:      RoleOperator,
		ExpiresAt: time.Now().Add(-time.Minute).Unix(),
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired signed token status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuthRejectsSignedTokenWithInvalidSignature(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys, privateKey := mustSignedTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	token := mustSignedToken(t, privateKey, SignedTokenClaims{
		Subject:   "worker.sig",
		Role:      RoleWorker,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	token += "tampered"

	pollBody := marshalJSON(t, workerPollRequest{WorkerID: "worker.smokevm"})
	req := httptest.NewRequest(http.MethodPost, "/v0/workers/poll", bytes.NewReader(pollBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered signed token status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestOperatorCanAccessThreadEndpoints(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

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

func mustSignedTestKeyStore(t *testing.T) (*KeyStore, ed25519.PrivateKey) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	keys, err := NewSignedKeyStore(base64.StdEncoding.EncodeToString(publicKey))
	if err != nil {
		t.Fatalf("NewSignedKeyStore() error = %v", err)
	}

	return keys, privateKey
}

func mustSignedToken(t *testing.T, privateKey ed25519.PrivateKey, claims SignedTokenClaims) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal(claims) error = %v", err)
	}

	signature := ed25519.Sign(privateKey, payload)
	return signedTokenPrefix +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}
