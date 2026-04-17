package runtime

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/store"
)

type nullbotIntakeRequest struct {
	Thread      model.Thread     `json:"thread"`
	Artifacts   []model.Artifact `json:"artifacts,omitempty"`
	InitialTask model.Envelope   `json:"initial_task"`
}

type nullbotIntakeResponse struct {
	Status   string               `json:"status"`
	ThreadID string               `json:"thread_id"`
	Snapshot store.ThreadSnapshot `json:"snapshot"`
}

func (s *server) handleNullbotIntake(w http.ResponseWriter, r *http.Request) {
	var request nullbotIntakeRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := validateNullbotIntake(request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	snapshot, err := s.store.CreateIntake(
		r.Context(),
		request.Thread,
		request.Artifacts,
		request.InitialTask,
	)
	switch {
	case errors.Is(err, store.ErrDuplicateThread),
		errors.Is(err, store.ErrDuplicateMessage),
		errors.Is(err, store.ErrDuplicateIdempotencyKey),
		errors.Is(err, store.ErrDuplicateArtifactID):
		writeError(w, http.StatusConflict, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusCreated, nullbotIntakeResponse{
			Status:   "accepted",
			ThreadID: request.Thread.ThreadID,
			Snapshot: snapshot,
		})
	}
}

func validateNullbotIntake(request nullbotIntakeRequest) error {
	if err := request.Thread.Validate(); err != nil {
		return err
	}
	if err := request.InitialTask.ValidateTaskRequest(); err != nil {
		return err
	}
	if request.InitialTask.ThreadID != request.Thread.ThreadID {
		return errors.New("initial_task thread_id must match thread.thread_id")
	}
	if !strings.Contains(strings.ToLower(request.Thread.Source), "nullbot") {
		return errors.New("thread source must identify nullbot intake")
	}
	if !strings.Contains(strings.ToLower(request.InitialTask.From), "nullbot") {
		return errors.New("initial_task from must identify nullbot collector")
	}
	if request.InitialTask.SentAt.Before(request.Thread.CreatedAt) {
		return errors.New("initial_task sent_at must not precede thread created_at")
	}
	for _, artifact := range request.Artifacts {
		if err := artifact.Validate(); err != nil {
			return err
		}
	}
	return nil
}
