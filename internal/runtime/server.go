package runtime

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/store"
)

type errorResponse struct {
	Error string `json:"error"`
}

type appendEnvelopeResponse struct {
	Status    string `json:"status"`
	ThreadID  string `json:"thread_id"`
	MessageID string `json:"message_id"`
}

type server struct {
	store *store.Store
}

func NewHandler(st *store.Store, keys *KeyStore) http.Handler {
	srv := &server{store: st}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", srv.handleHealthz)
	mux.HandleFunc("POST /v0/dispatch", withAuth(keys, RoleOperator, srv.handleDispatch))
	mux.HandleFunc("GET /v0/dispatch/{threadID}/resume", withAuth(keys, RoleOperator, srv.handleDispatchResume))
	mux.HandleFunc("POST /v0/threads", withAuth(keys, RoleOperator, srv.handleCreateThread))
	mux.HandleFunc("GET /v0/threads/{threadID}", withAuth(keys, RoleOperator, srv.handleGetThread))
	mux.HandleFunc("POST /v0/threads/{threadID}/messages", withAuth(keys, RoleOperator, srv.handleAppendEnvelope))
	mux.HandleFunc("POST /v0/workers/poll", withAuth(keys, RoleWorker, srv.handleWorkerPoll))
	mux.HandleFunc("POST /v0/workers/claim", withAuth(keys, RoleWorker, srv.handleWorkerClaim))
	mux.HandleFunc("POST /v0/workers/leases/{leaseID}/renew", withAuth(keys, RoleWorker, srv.handleLeaseRenew))
	mux.HandleFunc("POST /v0/workers/leases/{leaseID}/complete", withAuth(keys, RoleWorker, srv.handleLeaseComplete))

	return mux
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleCreateThread(w http.ResponseWriter, r *http.Request) {
	var thread model.Thread
	if err := decodeJSON(r.Body, &thread); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	snapshot, err := s.store.AppendThread(r.Context(), thread)
	switch {
	case errors.Is(err, store.ErrDuplicateThread):
		writeError(w, http.StatusConflict, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusCreated, snapshot)
	}
}

func (s *server) handleGetThread(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.store.LoadThread(r.Context(), r.PathValue("threadID"))
	switch {
	case errors.Is(err, store.ErrThreadNotFound):
		writeError(w, http.StatusNotFound, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, snapshot)
	}
}

func (s *server) handleAppendEnvelope(w http.ResponseWriter, r *http.Request) {
	threadID := r.PathValue("threadID")

	var envelope model.Envelope
	if err := decodeJSON(r.Body, &envelope); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if strings.TrimSpace(envelope.ThreadID) == "" {
		envelope.ThreadID = threadID
	}
	if envelope.ThreadID != threadID {
		writeError(w, http.StatusBadRequest, errors.New("thread_id does not match request path"))
		return
	}

	err := s.store.AppendEnvelope(r.Context(), envelope)
	switch {
	case errors.Is(err, store.ErrThreadNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrDuplicateMessage), errors.Is(err, store.ErrDuplicateIdempotencyKey):
		writeError(w, http.StatusConflict, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusAccepted, appendEnvelopeResponse{
			Status:    "accepted",
			ThreadID:  envelope.ThreadID,
			MessageID: envelope.MessageID,
		})
	}
}

func decodeJSON(body io.ReadCloser, target any) error {
	defer func() {
		_ = body.Close()
	}()

	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain a single json object")
		}
		return err
	}

	return nil
}

func isInputError(err error) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "required") ||
		strings.Contains(lower, "expected") ||
		strings.Contains(lower, "unsupported") ||
		strings.Contains(lower, "duplicate") ||
		strings.Contains(lower, "deadline must") ||
		strings.Contains(lower, "must match") ||
		strings.Contains(lower, "must be positive") ||
		strings.Contains(lower, "transition") ||
		strings.Contains(lower, "invalid")
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}

func currentTime() time.Time {
	return time.Now().UTC()
}
