package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
	"github.com/obstalabs/hivebus/internal/store"
)

const (
	dispatchRouteParticipantID = "agent.dispatch"
	dispatchRouteCapability    = "route.case"
	dispatchDefaultSource      = "hivebus.dispatch"
)

var dispatchIDCounter atomic.Uint64

type dispatchRequest struct {
	Signal          string                   `json:"signal,omitempty"`
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
	Type      model.ParticipantType `json:"participant_type,omitempty"`
	Kind      model.ParticipantKind `json:"kind,omitempty"`
	Name      string                `json:"display_name,omitempty"`
	SessionID string                `json:"session_id"`
	ThreadID  string                `json:"thread_id,omitempty"`
	MessageID string                `json:"message_id,omitempty"`
}

type dispatchPayload struct {
	Intent          *dispatchIntent          `json:"intent,omitempty"`
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
	Intent        *dispatchIntent      `json:"intent,omitempty"`
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
	Intent                 *dispatchIntent          `json:"intent,omitempty"`
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

type dispatchIntent struct {
	RawSignal   string   `json:"raw_signal"`
	Format      string   `json:"format"`
	Target      string   `json:"target"`
	Intent      string   `json:"intent"`
	TaskText    string   `json:"task_text"`
	Scope       string   `json:"scope,omitempty"`
	Priority    string   `json:"priority"`
	Context     string   `json:"context,omitempty"`
	Constraints []string `json:"constraints,omitempty"`
}

var signalTargetPattern = regexp.MustCompile(`^[A-Za-z0-9@._/-]+$`)

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
		intent, _ := dispatchIntentFromEnvelope(envelope)
		writeJSON(w, http.StatusCreated, dispatchResponse{
			Status:        "queued",
			ThreadID:      thread.ThreadID,
			TaskMessageID: envelope.MessageID,
			Intent:        intent,
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
	intent, resolved, err := resolveDispatchRequest(request)
	if err != nil {
		return model.Thread{}, model.Envelope{}, err
	}
	request = resolved

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
	if sender.Type == "" {
		switch sender.Kind {
		case model.ParticipantHuman:
			sender.Type = model.ParticipantTypeHuman
		case model.ParticipantService, model.ParticipantCollector:
			sender.Type = model.ParticipantTypeService
		default:
			sender.Type = model.ParticipantTypeAgent
		}
	}
	if sender.Kind == "" {
		sender.Kind = model.ParticipantAgent
	}

	senderParticipant, err := dispatchParticipantFromSender(sender)
	if err != nil {
		return model.Thread{}, model.Envelope{}, err
	}

	participants := []model.Participant{
		senderParticipant,
		{
			ID:           dispatchRouteParticipantID,
			Type:         model.ParticipantTypeService,
			Kind:         model.ParticipantService,
			DisplayName:  "Dispatch Router",
			Visibility:   model.ParticipantVisibilityInternal,
			Capabilities: []string{dispatchRouteCapability},
			Service: &model.ServiceParticipant{
				ServiceName: "dispatch-router",
			},
		},
	}
	if strings.TrimSpace(request.Target) != "" && request.Target != sender.ID {
		participants = append(participants, model.Participant{
			ID:          request.Target,
			Type:        model.ParticipantTypeAgent,
			Kind:        model.ParticipantAgent,
			DisplayName: request.Target,
			Visibility:  model.ParticipantVisibilityThread,
			Agent: &model.AgentParticipant{
				AgentID: request.Target,
			},
		})
	}

	payload, err := json.Marshal(dispatchPayload{
		Intent:          intent,
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

func dispatchParticipantFromSender(sender dispatchSender) (model.Participant, error) {
	participant := model.Participant{
		ID:          sender.ID,
		Type:        sender.Type,
		Kind:        sender.Kind,
		DisplayName: sender.Name,
		Visibility:  model.ParticipantVisibilityThread,
	}

	if participant.DisplayName == "" {
		participant.DisplayName = sender.ID
	}

	switch sender.Type {
	case model.ParticipantTypeHuman:
		participant.Human = &model.HumanParticipant{
			HumanID:            sender.ID,
			DeliveryPreference: model.HumanDeliveryInThread,
		}
	case model.ParticipantTypeService:
		participant.Service = &model.ServiceParticipant{
			ServiceName: sender.ID,
		}
	default:
		participant.Type = model.ParticipantTypeAgent
		participant.Agent = &model.AgentParticipant{
			AgentID: sender.ID,
		}
	}

	if err := participant.Validate(); err != nil {
		return model.Participant{}, fmt.Errorf("invalid sender participant: %w", err)
	}

	return participant, nil
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
		Intent:                 payload.Intent,
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

func dispatchIntentFromEnvelope(envelope model.Envelope) (*dispatchIntent, error) {
	var payload dispatchPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil, fmt.Errorf("invalid dispatch payload: %w", err)
	}

	return payload.Intent, nil
}

func resolveDispatchRequest(request dispatchRequest) (*dispatchIntent, dispatchRequest, error) {
	signal := strings.TrimSpace(request.Signal)
	if signal == "" {
		return nil, request, nil
	}

	if strings.TrimSpace(request.Title) != "" ||
		strings.TrimSpace(request.Task) != "" ||
		strings.TrimSpace(request.Target) != "" ||
		strings.TrimSpace(request.Capability) != "" ||
		len(request.Constraints) > 0 {
		return nil, dispatchRequest{}, errors.New("signal mode cannot be combined with explicit title, task, target, capability, or constraints")
	}

	intent, err := parseDispatchSignal(signal)
	if err != nil {
		return nil, dispatchRequest{}, err
	}
	if intent.Target == "@all" || intent.Target == "@best" {
		return nil, dispatchRequest{}, errors.New("signal target requires scheduler or broadcast support that is not implemented yet")
	}

	request.Title = titleFromIntent(intent)
	request.Task = taskFromIntent(intent)
	request.Target = intent.Target
	request.Constraints = append([]string{}, intent.Constraints...)

	return intent, request, nil
}

func parseDispatchSignal(signal string) (*dispatchIntent, error) {
	trimmed := strings.TrimSpace(signal)
	if !strings.HasPrefix(trimmed, "[hivebus]") {
		return nil, errors.New("signal must start with [hivebus]")
	}

	body := strings.TrimSpace(strings.TrimPrefix(trimmed, "[hivebus]"))
	if body == "" {
		return nil, errors.New("signal body is required")
	}

	if strings.Contains(body, "|") {
		return parseExtendedDispatchSignal(trimmed, body)
	}

	return parseSimpleDispatchSignal(trimmed, body)
}

func parseSimpleDispatchSignal(raw string, body string) (*dispatchIntent, error) {
	parts := strings.Fields(body)
	if len(parts) < 2 {
		return nil, errors.New("simple signal must include target and task text")
	}

	target := strings.TrimSpace(parts[0])
	if err := validateSignalTarget(target); err != nil {
		return nil, err
	}

	task := strings.TrimSpace(body[len(target):])
	if task == "" {
		return nil, errors.New("simple signal task text is required")
	}

	return &dispatchIntent{
		RawSignal: raw,
		Format:    "simple",
		Target:    target,
		Intent:    classifyIntent(task),
		TaskText:  task,
		Scope:     extractScope(task, ""),
		Priority:  extractPriority(task, nil),
	}, nil
}

func parseExtendedDispatchSignal(raw string, body string) (*dispatchIntent, error) {
	segments := strings.Split(body, "|")
	for i := range segments {
		segments[i] = strings.TrimSpace(segments[i])
	}
	if len(segments) < 2 {
		return nil, errors.New("extended signal must include target and intent segments")
	}

	target := segments[0]
	if err := validateSignalTarget(target); err != nil {
		return nil, err
	}
	if segments[1] == "" {
		return nil, errors.New("extended signal intent segment is required")
	}

	contextText := ""
	if len(segments) > 2 {
		contextText = segments[2]
	}

	constraints := []string{}
	if len(segments) > 3 {
		for _, segment := range segments[3:] {
			for _, part := range strings.Split(segment, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				constraints = append(constraints, part)
			}
		}
	}

	return &dispatchIntent{
		RawSignal:   raw,
		Format:      "extended",
		Target:      target,
		Intent:      classifyIntent(segments[1]),
		TaskText:    segments[1],
		Scope:       extractScope(segments[1], contextText),
		Priority:    extractPriority(segments[1]+" "+contextText, constraints),
		Context:     contextText,
		Constraints: constraints,
	}, nil
}

func validateSignalTarget(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return errors.New("signal target is required")
	}
	if !signalTargetPattern.MatchString(target) {
		return errors.New("signal target contains unsupported characters")
	}

	return nil
}

func classifyIntent(task string) string {
	lower := strings.ToLower(strings.TrimSpace(task))
	switch {
	case strings.HasPrefix(lower, "review "):
		return "review"
	case strings.HasPrefix(lower, "investigate "):
		return "investigate"
	case strings.HasPrefix(lower, "fix "):
		return "fix"
	case strings.HasPrefix(lower, "deploy "):
		return "deploy"
	case strings.HasPrefix(lower, "check "):
		return "check"
	case strings.HasPrefix(lower, "test "),
		strings.Contains(lower, "smoke test"),
		strings.Contains(lower, "smoke testing"),
		strings.Contains(lower, "go test"):
		return "test"
	case strings.HasPrefix(lower, "run "):
		return classifyIntent(strings.TrimSpace(strings.TrimPrefix(lower, "run ")))
	case strings.HasPrefix(lower, "do "):
		return classifyIntent(strings.TrimSpace(strings.TrimPrefix(lower, "do ")))
	default:
		fields := strings.Fields(lower)
		if len(fields) == 0 {
			return "execute"
		}
		return fields[0]
	}
}

func extractPriority(task string, constraints []string) string {
	candidates := append([]string{strings.ToLower(task)}, lowerStrings(constraints)...)
	for _, candidate := range candidates {
		switch {
		case strings.Contains(candidate, "priority=urgent"), strings.Contains(candidate, "urgent"):
			return "urgent"
		case strings.Contains(candidate, "priority=high"), strings.Contains(candidate, "high priority"):
			return "high"
		case strings.Contains(candidate, "priority=low"), strings.Contains(candidate, "low priority"):
			return "low"
		}
	}

	return "normal"
}

func extractScope(task string, contextText string) string {
	for _, candidate := range []string{contextText, task} {
		if scope := extractAssignment(candidate, "repo=", "project=", "scope="); scope != "" {
			return scope
		}
		if scope := extractFromPhrases(candidate); scope != "" {
			return scope
		}
	}

	return ""
}

func extractAssignment(text string, prefixes ...string) string {
	for _, field := range strings.Fields(text) {
		for _, prefix := range prefixes {
			if strings.HasPrefix(strings.ToLower(field), prefix) {
				return strings.TrimSpace(field[len(prefix):])
			}
		}
	}

	return ""
}

func extractFromPhrases(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	markers := []string{" of ", " on ", " in ", " for "}
	for _, marker := range markers {
		idx := strings.LastIndex(lower, marker)
		if idx == -1 {
			continue
		}

		candidate := strings.TrimSpace(trimmed[idx+len(marker):])
		candidate = cutAtConnector(candidate)
		if marker == " for " {
			if nested := strings.LastIndex(strings.ToLower(candidate), " of "); nested != -1 {
				candidate = strings.TrimSpace(candidate[nested+len(" of "):])
			}
		}
		if candidate != "" {
			return candidate
		}
	}

	return ""
}

func cutAtConnector(text string) string {
	connectors := []string{" and ", ",", ";", " with "}
	candidate := text
	lower := strings.ToLower(candidate)
	for _, connector := range connectors {
		if idx := strings.Index(lower, connector); idx != -1 {
			candidate = strings.TrimSpace(candidate[:idx])
			lower = strings.ToLower(candidate)
		}
	}

	return strings.TrimSpace(candidate)
}

func titleFromIntent(intent *dispatchIntent) string {
	if intent == nil {
		return "Dispatch task"
	}
	if intent.Scope != "" {
		return fmt.Sprintf("Dispatch %s for %s", intent.Intent, intent.Scope)
	}

	return fmt.Sprintf("Dispatch %s to %s", intent.Intent, intent.Target)
}

func taskFromIntent(intent *dispatchIntent) string {
	if intent == nil {
		return ""
	}

	task := strings.TrimSpace(intent.Intent)
	if strings.TrimSpace(intent.TaskText) != "" {
		task = strings.TrimSpace(intent.TaskText)
	} else if task == "" {
		task = "execute"
	}
	if intent.Scope != "" {
		task = task + " " + intent.Scope
	}
	if intent.Context != "" {
		task = task + " (" + intent.Context + ")"
	}

	return strings.TrimSpace(task)
}

func lowerStrings(values []string) []string {
	lowered := make([]string, 0, len(values))
	for _, value := range values {
		lowered = append(lowered, strings.ToLower(value))
	}

	return lowered
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
