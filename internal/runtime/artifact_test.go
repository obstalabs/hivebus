package runtime

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPutArtifactStoresManifestAndAssociatesThreadEvidence(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	artifacts := openTestArtifactStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, artifacts, keys)

	thread := sampleThread()
	mustCreateThreadWithAuth(t, handler, thread)

	uploadBody := marshalJSON(t, putArtifactRequest{
		ArtifactID: "art_smoke_log",
		Name:       "smoke.log",
		Kind:       "log",
		BodyBase64: base64.StdEncoding.EncodeToString([]byte("smoke output")),
	})
	uploadReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/threads/"+thread.ThreadID+"/artifacts",
		bytes.NewReader(uploadBody),
	)
	uploadReq.Header.Set("Content-Type", "application/json")
	uploadReq.Header.Set("Authorization", "Bearer worker-secret")
	uploadRec := httptest.NewRecorder()
	handler.ServeHTTP(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("POST /v0/threads/{id}/artifacts status = %d, body = %s", uploadRec.Code, uploadRec.Body.String())
	}

	var uploadResp putArtifactResponse
	if err := json.Unmarshal(uploadRec.Body.Bytes(), &uploadResp); err != nil {
		t.Fatalf("Unmarshal(upload) error = %v", err)
	}
	if uploadResp.Status != "stored" {
		t.Fatalf("unexpected upload response %#v", uploadResp)
	}
	if uploadResp.Artifact.URI == "" || uploadResp.Artifact.SHA256 == "" {
		t.Fatalf("expected stored artifact metadata, got %#v", uploadResp.Artifact)
	}

	threadReq := httptest.NewRequest(http.MethodGet, "/v0/threads/"+thread.ThreadID, nil)
	threadReq.Header.Set("Authorization", "Bearer operator-secret")
	threadRec := httptest.NewRecorder()
	handler.ServeHTTP(threadRec, threadReq)
	if threadRec.Code != http.StatusOK {
		t.Fatalf("GET /v0/threads/{id} status = %d, body = %s", threadRec.Code, threadRec.Body.String())
	}

	var snapshot struct {
		Thread struct {
			Evidence []struct {
				ArtifactID string `json:"artifact_id"`
				SHA256     string `json:"sha256"`
			} `json:"evidence"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(threadRec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("Unmarshal(snapshot) error = %v", err)
	}
	if len(snapshot.Thread.Evidence) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(snapshot.Thread.Evidence))
	}
	if snapshot.Thread.Evidence[0].ArtifactID != "art_smoke_log" {
		t.Fatalf("unexpected artifact id %q", snapshot.Thread.Evidence[0].ArtifactID)
	}
	if snapshot.Thread.Evidence[0].SHA256 != uploadResp.Manifest.SHA256 {
		t.Fatalf("expected digest %q, got %q", uploadResp.Manifest.SHA256, snapshot.Thread.Evidence[0].SHA256)
	}
}

func TestGetArtifactReturnsManifestAndBody(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	artifacts := openTestArtifactStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, artifacts, keys)

	thread := sampleThread()
	mustCreateThreadWithAuth(t, handler, thread)

	content := []byte("release smoke output")
	uploadBody := marshalJSON(t, putArtifactRequest{
		ArtifactID: "art_release_log",
		Name:       "release.log",
		Kind:       "log",
		BodyBase64: base64.StdEncoding.EncodeToString(content),
	})
	uploadReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/threads/"+thread.ThreadID+"/artifacts",
		bytes.NewReader(uploadBody),
	)
	uploadReq.Header.Set("Content-Type", "application/json")
	uploadReq.Header.Set("Authorization", "Bearer worker-secret")
	uploadRec := httptest.NewRecorder()
	handler.ServeHTTP(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", uploadRec.Code, uploadRec.Body.String())
	}

	var uploadResp putArtifactResponse
	if err := json.Unmarshal(uploadRec.Body.Bytes(), &uploadResp); err != nil {
		t.Fatalf("Unmarshal(upload) error = %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/v0/artifacts/"+uploadResp.Manifest.SHA256, nil)
	getReq.Header.Set("Authorization", "Bearer worker-secret")
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /v0/artifacts/{sha256} status = %d, body = %s", getRec.Code, getRec.Body.String())
	}

	var getResp getArtifactResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("Unmarshal(get) error = %v", err)
	}
	if getResp.Manifest.SHA256 != uploadResp.Manifest.SHA256 {
		t.Fatalf("expected manifest %q, got %q", uploadResp.Manifest.SHA256, getResp.Manifest.SHA256)
	}
	body, err := base64.StdEncoding.DecodeString(getResp.BodyBase64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	if !bytes.Equal(body, content) {
		t.Fatalf("expected body %q, got %q", string(content), string(body))
	}
}

func mustCreateThreadWithAuth(t *testing.T, handler http.Handler, thread any) {
	t.Helper()

	body := marshalJSON(t, thread)
	req := httptest.NewRequest(http.MethodPost, "/v0/threads", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer operator-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v0/threads status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
