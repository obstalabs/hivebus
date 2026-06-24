package runtime

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/obstalabs/hivebus/internal/artifact"
	"github.com/obstalabs/hivebus/internal/model"
	"github.com/obstalabs/hivebus/internal/store"
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
	store      *store.Store
	artifacts  *artifact.Store
	workOrders WorkOrderBridge
	syncHooks  map[string]ExecutionSyncHook
	now        func() time.Time // WO-161: test-only clock hook for exact HTTP fixtures.
}

func NewHandler(st *store.Store, artifacts *artifact.Store, keys *KeyStore) http.Handler {
	return NewHandlerWithOptions(st, artifacts, keys, HandlerOptions{})
}

func NewHandlerWithOptions(
	st *store.Store,
	artifacts *artifact.Store,
	keys *KeyStore,
	options HandlerOptions,
) http.Handler {
	now := options.Now
	if now == nil {
		now = currentTime
	}
	srv := &server{
		store:      st,
		artifacts:  artifacts,
		workOrders: options.WorkOrders,
		syncHooks:  options.SyncHooks,
		now:        now,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", srv.handleHealthz)
	mux.HandleFunc("POST /v0/agents/sessions/register", withAuth(keys, RoleWorker, srv.handleRegisterAgentSession))
	mux.HandleFunc("POST /v0/agents/sessions/heartbeat", withAuth(keys, RoleWorker, srv.handleHeartbeatAgentSession))
	mux.HandleFunc("GET /v0/agents/sessions", withAuth(keys, RoleWorker, srv.handleListAgentSessions))
	mux.HandleFunc("GET /v0/agents/sessions/{sessionID}/inbox", withAuth(keys, RoleWorker, srv.handlePeekAgentInbox))
	mux.HandleFunc("POST /v0/channels", withAuth(keys, RoleOperator, srv.handleUpsertChannel))
	mux.HandleFunc("GET /v0/channels/{channelID}", withAuth(keys, RoleOperator, srv.handleGetChannel))
	mux.HandleFunc("POST /v0/agents/messages/send", withAuth(keys, RoleOperator, srv.handleSendAgentMessage))
	mux.HandleFunc("GET /v0/agents/messages/{messageID}", withAuth(keys, RoleOperator, srv.handleGetAgentMessage))
	mux.HandleFunc("POST /v0/agents/messages/{messageID}/deliver", withAuth(keys, RoleWorker, srv.handleDeliverAgentMessage))
	mux.HandleFunc("POST /v0/intake/nullbot", withAuth(keys, RoleOperator, srv.handleNullbotIntake))
	mux.HandleFunc("POST /v0/dispatch", withAuth(keys, RoleOperator, srv.handleDispatch))
	mux.HandleFunc("GET /v0/dispatch/{threadID}/resume", withAuth(keys, RoleOperator, srv.handleDispatchResume))
	mux.HandleFunc("GET /v0/artifacts/{sha256}", withAuth(keys, RoleWorker, srv.handleGetArtifact))
	mux.HandleFunc("POST /v0/threads", withAuth(keys, RoleOperator, srv.handleCreateThread))
	mux.HandleFunc("GET /v0/threads/{threadID}", withAuth(keys, RoleOperator, srv.handleGetThread))
	mux.HandleFunc("GET /v0/threads/{threadID}/recovery", withAuth(keys, RoleOperator, srv.handleThreadRecovery))
	mux.HandleFunc("POST /v0/threads/{threadID}/promote", withAuth(keys, RoleOperator, srv.handlePromoteThread))
	mux.HandleFunc("POST /v0/threads/{threadID}/clarifications/response", withAuth(keys, RoleOperator, srv.handleClarificationResponse))
	mux.HandleFunc("GET /v0/threads/{threadID}/watch", withAuth(keys, RoleWorker, srv.handleWatchThread))
	mux.HandleFunc("POST /v0/threads/{threadID}/artifacts", withAuth(keys, RoleWorker, srv.handlePutArtifact))
	mux.HandleFunc("POST /v0/threads/{threadID}/messages", withAuth(keys, RoleOperator, srv.handleAppendEnvelope))
	mux.HandleFunc("GET /v0/workers/{workerID}/capabilities", withAuth(keys, RoleOperator, srv.handleListWorkerCapabilities))
	mux.HandleFunc("POST /v0/workers/{workerID}/capabilities/{capabilityID}/approve", withAuth(keys, RoleOperator, srv.handleApproveWorkerCapability))
	mux.HandleFunc("POST /v0/workers/poll", withAuth(keys, RoleWorker, srv.handleWorkerPoll))
	mux.HandleFunc("POST /v0/workers/claim", withAuth(keys, RoleWorker, srv.handleWorkerClaim))
	mux.HandleFunc("POST /v0/workers/leases/{leaseID}/clarifications/request", withAuth(keys, RoleWorker, srv.handleClarificationRequest))
	mux.HandleFunc("POST /v0/workers/leases/{leaseID}/renew", withAuth(keys, RoleWorker, srv.handleLeaseRenew))
	mux.HandleFunc("POST /v0/workers/leases/{leaseID}/partial", withAuth(keys, RoleWorker, srv.handleLeasePartial))
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
	if err := store.ValidateThreadAppendEnvelope(envelope); err != nil {
		writeError(w, http.StatusBadRequest, err)
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
		strings.Contains(lower, "must be a") ||
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

func (s *server) currentTime() time.Time {
	if s == nil || s.now == nil {
		return currentTime()
	}
	return s.now().UTC()
}
