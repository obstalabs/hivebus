package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Tier represents the commercial policy surface for a tenant.
type Tier string

const (
	TierFree       Tier = "free"
	TierPro        Tier = "pro"
	TierTeams      Tier = "teams"
	TierEnterprise Tier = "enterprise"
)

var validTiers = []Tier{
	TierFree,
	TierPro,
	TierTeams,
	TierEnterprise,
}

// ThreadStatus describes where an intake/investigation thread sits in the workflow.
type ThreadStatus string

const (
	ThreadStatusReported      ThreadStatus = "reported"
	ThreadStatusCollecting    ThreadStatus = "collecting"
	ThreadStatusInvestigating ThreadStatus = "investigating"
	ThreadStatusWaiting       ThreadStatus = "waiting_for_input"
	ThreadStatusReadyForWork  ThreadStatus = "ready_for_work_order"
	ThreadStatusDone          ThreadStatus = "done"
	ThreadStatusFailed        ThreadStatus = "failed"
	ThreadStatusCancelled     ThreadStatus = "cancelled"
)

var validThreadStatuses = []ThreadStatus{
	ThreadStatusReported,
	ThreadStatusCollecting,
	ThreadStatusInvestigating,
	ThreadStatusWaiting,
	ThreadStatusReadyForWork,
	ThreadStatusDone,
	ThreadStatusFailed,
	ThreadStatusCancelled,
}

var allowedTransitions = map[ThreadStatus][]ThreadStatus{
	ThreadStatusReported: {
		ThreadStatusCollecting,
		ThreadStatusCancelled,
		ThreadStatusFailed,
	},
	ThreadStatusCollecting: {
		ThreadStatusInvestigating,
		ThreadStatusWaiting,
		ThreadStatusCancelled,
		ThreadStatusFailed,
	},
	ThreadStatusInvestigating: {
		ThreadStatusWaiting,
		ThreadStatusReadyForWork,
		ThreadStatusCancelled,
		ThreadStatusFailed,
	},
	ThreadStatusWaiting: {
		ThreadStatusCollecting,
		ThreadStatusInvestigating,
		ThreadStatusCancelled,
		ThreadStatusFailed,
	},
	ThreadStatusReadyForWork: {
		ThreadStatusDone,
		ThreadStatusCancelled,
		ThreadStatusFailed,
	},
}

// Thread groups messages, evidence, and lifecycle around a single user story.
type Thread struct {
	ThreadID     string        `json:"thread_id"`
	Title        string        `json:"title"`
	Status       ThreadStatus  `json:"status"`
	CustomerTier Tier          `json:"customer_tier"`
	Source       string        `json:"source"`
	Summary      string        `json:"summary,omitempty"`
	Participants []Participant `json:"participants"`
	Evidence     []Artifact    `json:"evidence,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

type PendingClarification struct {
	TaskMessageID string    `json:"task_message_id"`
	TaskRequest   Envelope  `json:"task_request"`
	Request       Envelope  `json:"request"`
	Expired       bool      `json:"expired"`
	Deadline      time.Time `json:"deadline"`
}

// Validate applies the non-negotiable structural guarantees for a thread.
func (t Thread) Validate() error {
	switch {
	case strings.TrimSpace(t.ThreadID) == "":
		return errors.New("thread_id is required")
	case strings.TrimSpace(t.Title) == "":
		return errors.New("title is required")
	case !slices.Contains(validThreadStatuses, t.Status):
		return fmt.Errorf("unsupported status %q", t.Status)
	case !slices.Contains(validTiers, t.CustomerTier):
		return fmt.Errorf("unsupported customer_tier %q", t.CustomerTier)
	case strings.TrimSpace(t.Source) == "":
		return errors.New("source is required")
	case len(t.Participants) == 0:
		return errors.New("at least one participant is required")
	case t.CreatedAt.IsZero():
		return errors.New("created_at is required")
	case t.UpdatedAt.IsZero():
		return errors.New("updated_at is required")
	case t.UpdatedAt.Before(t.CreatedAt):
		return errors.New("updated_at must not be before created_at")
	}

	seenParticipants := make(map[string]struct{}, len(t.Participants))
	for _, participant := range t.Participants {
		if err := participant.Validate(); err != nil {
			return fmt.Errorf("invalid participant: %w", err)
		}
		participantID := strings.TrimSpace(participant.ID)

		if _, exists := seenParticipants[participantID]; exists {
			return fmt.Errorf("duplicate participant %q", participantID)
		}

		seenParticipants[participantID] = struct{}{}
	}

	seenArtifacts := make(map[string]struct{}, len(t.Evidence))
	for _, artifact := range t.Evidence {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("invalid evidence: %w", err)
		}
		if _, exists := seenArtifacts[artifact.ArtifactID]; exists {
			return fmt.Errorf("duplicate evidence artifact %q", artifact.ArtifactID)
		}
		seenArtifacts[artifact.ArtifactID] = struct{}{}
	}

	return nil
}

// Transition moves the thread between allowed states and updates UpdatedAt.
func (t *Thread) Transition(next ThreadStatus, at time.Time) error {
	if err := t.Validate(); err != nil {
		return err
	}

	if !slices.Contains(validThreadStatuses, next) {
		return fmt.Errorf("unsupported next status %q", next)
	}

	if at.IsZero() {
		return errors.New("transition time is required")
	}

	if at.Before(t.UpdatedAt) {
		return errors.New("transition time must not go backwards")
	}

	if t.Status == next {
		t.UpdatedAt = at
		return nil
	}

	allowed := allowedTransitions[t.Status]
	if !slices.Contains(allowed, next) {
		return fmt.Errorf("cannot transition from %s to %s", t.Status, next)
	}

	t.Status = next
	t.UpdatedAt = at

	return nil
}

func (t Thread) PendingClarification(
	envelopes []Envelope,
	now time.Time,
) (*PendingClarification, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	taskRequests := make(map[string]Envelope)
	clarificationRequests := make(map[string]Envelope)
	requestOrder := make([]string, 0)
	resolved := make(map[string]struct{})

	for _, envelope := range envelopes {
		switch envelope.Type {
		case MessageTypeTaskRequest:
			taskRequests[envelope.MessageID] = envelope
		case MessageTypeClarifyRequest:
			taskRequest, ok := taskRequests[strings.TrimSpace(envelope.ReplyTo)]
			if !ok {
				return nil, errors.New("clarification.request reply_to must match an existing task.request")
			}
			if err := envelope.ValidateClarificationRequest(taskRequest); err != nil {
				return nil, err
			}
			clarificationRequests[envelope.MessageID] = envelope
			requestOrder = append(requestOrder, envelope.MessageID)
		case MessageTypeClarifyResponse:
			request, ok := clarificationRequests[strings.TrimSpace(envelope.ReplyTo)]
			if !ok {
				return nil, errors.New("clarification.response reply_to must match an existing clarification.request")
			}
			taskRequest, ok := taskRequests[strings.TrimSpace(request.ReplyTo)]
			if !ok {
				return nil, errors.New("clarification.request reply_to must match an existing task.request")
			}
			if err := envelope.ValidateClarificationResponse(taskRequest, request); err != nil {
				return nil, err
			}
			resolved[request.MessageID] = struct{}{}
		}
	}

	var pending *PendingClarification
	for _, requestID := range requestOrder {
		request := clarificationRequests[requestID]
		if _, ok := resolved[requestID]; ok {
			continue
		}
		taskRequest := taskRequests[strings.TrimSpace(request.ReplyTo)]
		if pending != nil {
			return nil, errors.New("multiple pending clarification requests are not supported")
		}
		state := PendingClarification{
			TaskMessageID: taskRequest.MessageID,
			TaskRequest:   taskRequest,
			Request:       request,
			Deadline:      request.Deadline.UTC(),
			Expired:       request.Deadline.Before(now),
		}
		pending = &state
	}

	return pending, nil
}

func (t Thread) HasPendingClarification(
	taskMessageID string,
	envelopes []Envelope,
	now time.Time,
) (bool, error) {
	state, err := t.PendingClarification(envelopes, now)
	if err != nil {
		return false, err
	}
	if state == nil {
		return false, nil
	}

	return strings.TrimSpace(state.TaskMessageID) == strings.TrimSpace(taskMessageID), nil
}
