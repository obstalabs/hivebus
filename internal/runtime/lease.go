package runtime

import (
	"errors"
	"net/http"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/store"
)

type leaseRenewRequest struct {
	WorkerID     string `json:"worker_id"`
	LeaseSeconds int    `json:"lease_seconds,omitempty"`
}

type leaseCompleteRequest struct {
	WorkerID       string         `json:"worker_id"`
	ResultEnvelope model.Envelope `json:"result_envelope"`
}

type leasePartialRequest struct {
	WorkerID       string         `json:"worker_id"`
	ResultEnvelope model.Envelope `json:"result_envelope"`
}

type leaseResponse struct {
	Status string      `json:"status"`
	Lease  store.Lease `json:"lease"`
}

func (s *server) handleLeaseRenew(w http.ResponseWriter, r *http.Request) {
	var request leaseRenewRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	leaseDuration := defaultLeaseDuration
	if request.LeaseSeconds != 0 {
		leaseDuration = time.Duration(request.LeaseSeconds) * time.Second
	}

	lease, err := s.store.RenewLease(
		r.Context(),
		r.PathValue("leaseID"),
		request.WorkerID,
		leaseDuration,
		currentTime(),
	)
	switch {
	case errors.Is(err, store.ErrLeaseNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrLeaseExpired), errors.Is(err, store.ErrLeaseFinalized):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, store.ErrLeaseNotOwned):
		writeError(w, http.StatusForbidden, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, leaseResponse{
			Status: "renewed",
			Lease:  lease,
		})
	}
}

func (s *server) handleLeaseComplete(w http.ResponseWriter, r *http.Request) {
	var request leaseCompleteRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	lease, err := s.store.CompleteLease(
		r.Context(),
		r.PathValue("leaseID"),
		request.WorkerID,
		request.ResultEnvelope,
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
		writeJSON(w, http.StatusOK, leaseResponse{
			Status: "completed",
			Lease:  lease,
		})
	}
}

func (s *server) handleLeasePartial(w http.ResponseWriter, r *http.Request) {
	var request leasePartialRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	err := s.store.AppendLeaseResultPart(
		r.Context(),
		r.PathValue("leaseID"),
		request.WorkerID,
		request.ResultEnvelope,
		currentTime(),
	)
	switch {
	case errors.Is(err, store.ErrLeaseNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrLeaseExpired), errors.Is(err, store.ErrLeaseFinalized):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, store.ErrLeaseNotOwned):
		writeError(w, http.StatusForbidden, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "streaming"})
	}
}
