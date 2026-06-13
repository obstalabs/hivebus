package runtime

import (
	"errors"
	"net/http"

	"github.com/obstalabs/hivebus/internal/model"
	"github.com/obstalabs/hivebus/internal/store"
)

type workerCapabilitiesResponse struct {
	Status       string                       `json:"status"`
	WorkerID     string                       `json:"worker_id"`
	Capabilities []model.DiscoveredCapability `json:"capabilities"`
}

type approveCapabilityRequest struct {
	ApprovedBy string `json:"approved_by"`
}

type approveCapabilityResponse struct {
	Status     string                     `json:"status"`
	Capability model.DiscoveredCapability `json:"capability"`
}

func (s *server) handleListWorkerCapabilities(w http.ResponseWriter, r *http.Request) {
	workerID := r.PathValue("workerID")
	capabilities, err := s.store.ListDiscoveredCapabilities(r.Context(), workerID, currentTime())
	switch {
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, workerCapabilitiesResponse{
			Status:       "ok",
			WorkerID:     workerID,
			Capabilities: capabilities,
		})
	}
}

func (s *server) handleApproveWorkerCapability(w http.ResponseWriter, r *http.Request) {
	var request approveCapabilityRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	record, err := s.store.ApproveDiscoveredCapability(
		r.Context(),
		r.PathValue("workerID"),
		r.PathValue("capabilityID"),
		request.ApprovedBy,
		currentTime(),
	)
	switch {
	case errors.Is(err, store.ErrDiscoveredCapabilityNotFound):
		writeError(w, http.StatusNotFound, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, approveCapabilityResponse{
			Status:     "approved",
			Capability: record,
		})
	}
}
