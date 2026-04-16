package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/store"
)

const (
	dispatchRouteParticipantID = "agent.dispatch"
	dispatchRouteCapability    = "route.case"
	dispatchDefaultSource      = "hivebus.dispatch"
)

var dispatchIDCounter atomic.Uint64

type dispatchRequest struct {
	Title           string                   `json:"title"`
	Task            string                   `json:"task"`
	Target          string                   `json:"target,omitempty"`
	Capability      string                   `json:"capability,omitempty"`
	CustomerTier    model.Tier               `json:"customer_tier,omitempty"`
	Source          string                   `json:"source,omitempty"`
	Sender          dispatchSender           `json:"sender"`
	ContextSnapshot *dispatchContextSnapshot `json:"context_snapshot,omitempty"`
	Constraints     []string                 `json:"constraints,omitempty"`
	ArtifactIDs     []string                 `json:"artifact_ids,omitempty"`
}

type dispatchSender struct {
	ID        string                `json:"id"`
	Kind      model.ParticipantKind `json:"kind,omitempty"`
	SessionID string                `json:"session_id"`
	ThreadID  string                `json:"thread_id,omitempty"`
	MessageID string                `json:"message_id,omitempty"`
}

type dispatchPayload struct {
	Task            string                   `json:"task"`
	Target          string                   `json:"target,omitempty"`
	Capability      string                   `json:"capability,omitempty"`
	Sender          dispatchSender           `json:"sender"`
	ContextSnapshot *dispatchContextSnapshot `json:"context_snapshot,omitempty"`
	Constraints     []string                 `json:"constraints,omitempty"`
}

type dispatchResponse struct {
	Status        string               `json:"status"`
	ThreadID      string               `json:"thread_id"`
	TaskMessageID string               `json:"task_message_id"`
	Snapshot      store.ThreadSnapshot `json:"snapshot"`
}

type dispatchContextSnapshot struct {
	CWD               string   `json:"cwd,omitempty"`
	Repo              string   `json:"repo,omitempty"`
	GitBranch         string   `json:"git_branch,omitempty"`
	GitHead           string   `json:"git_head,omitempty"`
	WorkledgerProject string   `json:"workledger_project,omitempty"`
	RelevantPaths     []string `json:"relevant_paths,omitempty"`
	TestCommands      []string `json:"test_commands,omitempty"`
}

type resumeCapsule struct {
	ThreadID               string                   `json:"thread_id"`
	TaskMessageID          string                   `json:"task_message_id"`
	TargetAlias            string                   `json:"target_alias,omitempty"`
	TransportKind          string                   `json:"transport_kind"`
	Project                string                   `json:"project,omitempty"`
	Repo                   string                   `json:"repo,omitempty"`
	State                  string                   `json:"state"`
	LastEventAt            string                   `json:"last_event_at,omitempty"`
	NextStep               string                   `json:"next_step"`
	OperatorActionRequired bool                     `json:"operator_action_required"`
	SenderSessionID        string                   `json:"sender_session_id"`
	ContextSnapshot        *dispatchContextSnapshot `json:"context_snapshot,omitempty"`
}

func (s *server) handleDispatch(w http.ResponseWriter, r *http.Request) {
	var request dispatchRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	thread, envelope, err := buildDispatch(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	snapshot, err := s.store.CreateDispatch(r.Context(), thread, envelope)
	switch {
	case errors.Is(err, store.ErrDuplicateThread), errors.Is(err, store.ErrDuplicateMessage), errors.Is(err, store.ErrDuplicateIdempotencyKey):
		writeError(w, http.StatusConflict, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusCreated, dispatchResponse{
			Status:        "queued",
			ThreadID:      thread.ThreadID,
			TaskMessageID: envelope.MessageID,
			Snapshot:      snapshot,
		})
	}
}

func (s *server) handleDispatchResume(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.store.LoadThread(r.Context(), r.PathValue("threadID"))
	switch {
	case errors.Is(err, store.ErrThreadNotFound):
		writeError(w, http.StatusNotFound, err)
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	capsule, err := buildResumeCapsule(snapshot)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	writeJSON(w, http.StatusOK, capsule)
}

func buildDispatch(request dispatchRequest) (model.Thread, model.Envelope, error) {
	if strings.TrimSpace(request.Title) == "" {
		return model.Thread{}, model.Envelope{}, errors.New("title is required")
	}
	if strings.TrimSpace(request.Task) == "" {
		return model.Thread{}, model.Envelope{}, errors.New("task is required")
	}
	if strings.TrimSpace(request.Sender.ID) == "" {
		return model.Thread{}, model.Envelope{}, errors.New("sender.id is required")
	}
	if strings.TrimSpace(request.Sender.SessionID) == "" {
		return model.Thread{}, model.Envelope{}, errors.New("sender.session_id is required")
	}
	if strings.TrimSpace(request.Target) == "" && strings.TrimSpace(request.Capability) == "" {
		return model.Thread{}, model.Envelope{}, errors.New("target or capability is required")
	}
	if strings.TrimSpace(request.Target) != "" && strings.TrimSpace(request.Capability) != "" {
		return model.Thread{}, model.Envelope{}, errors.New("target and capability are mutually exclusive")
	}
	now := currentTime()
	threadID := newDispatchID("thr_dispatch")
	messageID := newDispatchID("msg_dispatch")

	customerTier := request.CustomerTier
	if customerTier == "" {
		customerTier = model.TierPro
	}

	source := strings.TrimSpace(request.Source)
	if source == "" {
		source = dispatchDefaultSource
	}

	sender := request.Sender
	if sender.Kind == "" {
		sender.Kind = model.ParticipantAgent
	}

	participants := []model.Participant{
		{
			ID:   sender.ID,
			Kind: sender.Kind,
		},
		{
			ID:           dispatchRouteParticipantID,
			Kind:         model.ParticipantService,
			Capabilities: []string{dispatchRouteCapability},
		},
	}
	if strings.TrimSpace(request.Target) != "" && request.Target != sender.ID {
		participants = append(participants, model.Participant{
			ID:   request.Target,
			Kind: model.ParticipantAgent,
		})
	}

	payload, err := json.Marshal(dispatchPayload{
		Task:            request.Task,
		Target:          request.Target,
		Capability:      request.Capability,
		Sender:          sender,
		ContextSnapshot: request.ContextSnapshot,
		Constraints:     request.Constraints,
	})
	if err != nil {
		return model.Thread{}, model.Envelope{}, fmt.Errorf("marshal dispatch payload: %w", err)
	}

	thread := model.Thread{
		ThreadID:     threadID,
		Title:        request.Title,
		Status:       model.ThreadStatusReadyForWork,
		CustomerTier: customerTier,
		Source:       source,
		Summary:      request.Task,
		Participants: participants,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	envelope := model.Envelope{
		MessageID:      messageID,
		ThreadID:       threadID,
		From:           sender.ID,
		Type:           model.MessageTypeTaskRequest,
		Capability:     strings.TrimSpace(request.Capability),
		Payload:        payload,
		ArtifactIDs:    request.ArtifactIDs,
		SentAt:         now,
		IdempotencyKey: newDispatchID("idem_dispatch"),
		Trace: model.Trace{
			CorrelationID: threadID,
			SpanID:        "dispatch",
			Model:         source,
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "internal",
			Nonce:  newDispatchID("nonce"),
			Signed: false,
		},
	}
	if strings.TrimSpace(request.Target) != "" {
		envelope.To = []string{request.Target}
	}

	return thread, envelope, nil
}

func buildResumeCapsule(snapshot store.ThreadSnapshot) (resumeCapsule, error) {
	request, payload, err := dispatchTaskFromSnapshot(snapshot)
	if err != nil {
		return resumeCapsule{}, err
	}

	state, nextStep, lastEventAt := summarizeDispatchState(snapshot, request)
	project := ""
	repo := ""
	if payload.ContextSnapshot != nil {
		project = payload.ContextSnapshot.WorkledgerProject
		repo = payload.ContextSnapshot.Repo
	}

	targetAlias := payload.Target
	if targetAlias == "" && len(request.To) > 0 {
		targetAlias = request.To[0]
	}
	if targetAlias == "" {
		targetAlias = request.Capability
	}

	capsule := resumeCapsule{
		ThreadID:               snapshot.Thread.ThreadID,
		TaskMessageID:          request.MessageID,
		TargetAlias:            targetAlias,
		TransportKind:          "agent",
		Project:                project,
		Repo:                   repo,
		State:                  state,
		NextStep:               nextStep,
		OperatorActionRequired: false,
		SenderSessionID:        payload.Sender.SessionID,
		ContextSnapshot:        payload.ContextSnapshot,
	}
	if !lastEventAt.IsZero() {
		capsule.LastEventAt = lastEventAt.Format(time.RFC3339Nano)
	}

	return capsule, nil
}

func dispatchTaskFromSnapshot(snapshot store.ThreadSnapshot) (model.Envelope, dispatchPayload, error) {
	for _, envelope := range snapshot.Envelopes {
		if envelope.Type != model.MessageTypeTaskRequest {
			continue
		}

		var payload dispatchPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return model.Envelope{}, dispatchPayload{}, fmt.Errorf("invalid dispatch payload: %w", err)
		}
		if strings.TrimSpace(payload.Sender.SessionID) == "" {
			return model.Envelope{}, dispatchPayload{}, errors.New("dispatch payload sender.session_id is required")
		}

		return envelope, payload, nil
	}

	return model.Envelope{}, dispatchPayload{}, errors.New("thread does not contain a dispatched task.request envelope")
}

func summarizeDispatchState(snapshot store.ThreadSnapshot, request model.Envelope) (string, string, time.Time) {
	lastEventAt := request.SentAt
	state := "queued"
	nextStep := "matching worker should poll and claim the task"

	for _, receipt := range snapshot.LeaseReceipts {
		if receipt.TaskMessageID != request.MessageID {
			continue
		}
		if receipt.At.After(lastEventAt) {
			lastEventAt = receipt.At
		}

		switch receipt.Action {
		case store.LeaseReceiptClaimed, store.LeaseReceiptRenewed:
			state = "leased"
			nextStep = "wait for the worker to complete or renew the active lease"
		case store.LeaseReceiptExpired:
			state = "expired"
			nextStep = "matching worker should reclaim the expired task"
		case store.LeaseReceiptCompleted:
			state = "completed"
			nextStep = "review the final result and relay it back to the sender session"
		}
	}

	for _, envelope := range snapshot.Envelopes {
		if envelope.SentAt.After(lastEventAt) {
			lastEventAt = envelope.SentAt
		}
	}

	return state, nextStep, lastEventAt
}

func newDispatchID(prefix string) string {
	return fmt.Sprintf("%s_%d_%06d", prefix, currentTime().UnixNano(), dispatchIDCounter.Add(1))
}
