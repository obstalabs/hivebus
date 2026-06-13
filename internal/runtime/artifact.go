package runtime

import (
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	artifactstore "github.com/obstalabs/hivebus/internal/artifact"
	"github.com/obstalabs/hivebus/internal/model"
	"github.com/obstalabs/hivebus/internal/store"
)

type putArtifactRequest struct {
	ArtifactID  string `json:"artifact_id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	ContentType string `json:"content_type,omitempty"`
	BodyBase64  string `json:"body_base64"`
	Redacted    bool   `json:"redacted"`
}

type putArtifactResponse struct {
	Status   string                 `json:"status"`
	ThreadID string                 `json:"thread_id"`
	Artifact model.Artifact         `json:"artifact"`
	Manifest artifactstore.Manifest `json:"manifest"`
}

type getArtifactResponse struct {
	Manifest   artifactstore.Manifest `json:"manifest"`
	BodyBase64 string                 `json:"body_base64"`
}

func (s *server) handlePutArtifact(w http.ResponseWriter, r *http.Request) {
	if s.artifacts == nil {
		writeError(w, http.StatusInternalServerError, errors.New("artifact store is not configured"))
		return
	}

	threadID := r.PathValue("threadID")
	if _, err := s.store.LoadThread(r.Context(), threadID); err != nil {
		switch {
		case errors.Is(err, store.ErrThreadNotFound):
			writeError(w, http.StatusNotFound, err)
		default:
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}

	var request putArtifactRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	body, err := decodeArtifactBody(request.BodyBase64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	manifest, err := s.artifacts.Put(r.Context(), artifactstore.PutInput{
		ContentType: request.ContentType,
		Body:        body,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	artifact := model.Artifact{
		ArtifactID:  strings.TrimSpace(request.ArtifactID),
		Name:        strings.TrimSpace(request.Name),
		Kind:        strings.TrimSpace(request.Kind),
		URI:         artifactstore.URIForDigest(manifest.SHA256),
		SHA256:      manifest.SHA256,
		SizeBytes:   manifest.SizeBytes,
		ContentType: manifest.ContentType,
		Redacted:    request.Redacted,
	}

	err = s.store.AppendArtifact(r.Context(), threadID, artifact, currentTime().UTC().Format(time.RFC3339Nano))
	switch {
	case errors.Is(err, store.ErrThreadNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrDuplicateArtifactID):
		writeError(w, http.StatusConflict, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusCreated, putArtifactResponse{
			Status:   "stored",
			ThreadID: threadID,
			Artifact: artifact,
			Manifest: manifest,
		})
	}
}

func (s *server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	if s.artifacts == nil {
		writeError(w, http.StatusInternalServerError, errors.New("artifact store is not configured"))
		return
	}

	manifest, body, err := s.artifacts.Get(r.Context(), r.PathValue("sha256"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, getArtifactResponse{
			Manifest:   manifest,
			BodyBase64: base64.StdEncoding.EncodeToString(body),
		})
	}
}

func decodeArtifactBody(bodyBase64 string) ([]byte, error) {
	if strings.TrimSpace(bodyBase64) == "" {
		return nil, errors.New("body_base64 is required")
	}

	body, err := base64.StdEncoding.DecodeString(bodyBase64)
	if err != nil {
		return nil, errors.New("body_base64 must be valid base64")
	}

	return body, nil
}
