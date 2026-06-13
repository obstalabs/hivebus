package runtime

import (
	"errors"
	"net/http"

	"github.com/obstalabs/hivebus/internal/store"
)

func (s *server) handleThreadRecovery(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.store.LoadThread(r.Context(), r.PathValue("threadID"))
	switch {
	case errors.Is(err, store.ErrThreadNotFound):
		writeError(w, http.StatusNotFound, err)
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	capsule, err := store.BuildPromotedThreadRecoveryCapsule(snapshot, currentTime())
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, capsule)
}
