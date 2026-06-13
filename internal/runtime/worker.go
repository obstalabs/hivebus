package runtime

import (
	"errors"
	"net/http"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
	"github.com/obstalabs/hivebus/internal/store"
)

const defaultLeaseDuration = 5 * time.Minute

type workerPollRequest struct {
	WorkerID     string   `json:"worker_id"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type workerClaimRequest struct {
	WorkerID         string         `json:"worker_id"`
	TaskMessageID    string         `json:"task_message_id"`
	AcceptedEnvelope model.Envelope `json:"accepted_envelope"`
	LeaseSeconds     int            `json:"lease_seconds,omitempty"`
}

type workerPollResponse struct {
	Status string            `json:"status"`
	Task   *store.WorkerTask `json:"task,omitempty"`
	Lease  *store.Lease      `json:"lease,omitempty"`
}

type workerClaimResponse struct {
	Status string      `json:"status"`
	Lease  store.Lease `json:"lease"`
}

func (s *server) handleWorkerPoll(w http.ResponseWriter, r *http.Request) {
	var request workerPollRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result, err := s.store.PollTask(r.Context(), request.WorkerID, request.Capabilities, currentTime())
	switch {
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, workerPollResponse{
			Status: string(result.Status),
			Task:   result.Task,
			Lease:  result.Lease,
		})
	}
}

func (s *server) handleWorkerClaim(w http.ResponseWriter, r *http.Request) {
	var request workerClaimRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	leaseDuration := defaultLeaseDuration
	if request.LeaseSeconds != 0 {
		leaseDuration = time.Duration(request.LeaseSeconds) * time.Second
	}

	lease, err := s.store.ClaimTask(
		r.Context(),
		request.WorkerID,
		request.TaskMessageID,
		request.AcceptedEnvelope,
		leaseDuration,
		currentTime(),
	)
	switch {
	case errors.Is(err, store.ErrTaskNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrTaskAlreadyClaimed), errors.Is(err, store.ErrTaskCompleted), errors.Is(err, store.ErrWorkerBusy):
		writeError(w, http.StatusConflict, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusAccepted, workerClaimResponse{
			Status: "claimed",
			Lease:  lease,
		})
	}
}
