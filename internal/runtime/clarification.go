package runtime

import (
	"errors"
	"net/http"

	"github.com/obstalabs/hivebus/internal/model"
	"github.com/obstalabs/hivebus/internal/store"
)

type clarificationRequestPayload struct {
	WorkerID string         `json:"worker_id"`
	Request  model.Envelope `json:"request_envelope"`
}

type clarificationResponsePayload struct {
	Response model.Envelope `json:"response_envelope"`
}

func (s *server) handleClarificationRequest(w http.ResponseWriter, r *http.Request) {
	var request clarificationRequestPayload
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	err := s.store.AppendClarificationRequest(
		r.Context(),
		r.PathValue("leaseID"),
		request.WorkerID,
		request.Request,
		currentTime(),
	)
	switch {
	case errors.Is(err, store.ErrLeaseNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrLeaseExpired), errors.Is(err, store.ErrLeaseFinalized),
		errors.Is(err, store.ErrClarificationPending):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, store.ErrLeaseNotOwned):
		writeError(w, http.StatusForbidden, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "waiting_for_input"})
	}
}

func (s *server) handleClarificationResponse(w http.ResponseWriter, r *http.Request) {
	var request clarificationResponsePayload
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	err := s.store.AppendClarificationResponse(
		r.Context(),
		r.PathValue("threadID"),
		request.Response,
		currentTime(),
	)
	switch {
	case errors.Is(err, store.ErrThreadNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrClarificationExpired), errors.Is(err, store.ErrClarificationStale):
		writeError(w, http.StatusConflict, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "resolved"})
	}
}
