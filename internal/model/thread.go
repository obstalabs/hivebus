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

// ParticipantKind classifies who is active in a thread.
type ParticipantKind string

const (
	ParticipantCollector ParticipantKind = "collector"
	ParticipantAgent     ParticipantKind = "agent"
	ParticipantHuman     ParticipantKind = "human"
	ParticipantService   ParticipantKind = "service"
)

// Participant is any actor with a stable identity inside a thread.
type Participant struct {
	ID           string          `json:"id"`
	Kind         ParticipantKind `json:"kind"`
	Capabilities []string        `json:"capabilities,omitempty"`
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
		participantID := strings.TrimSpace(participant.ID)
		if participantID == "" {
			return errors.New("participant id is required")
		}

		if _, exists := seenParticipants[participantID]; exists {
			return fmt.Errorf("duplicate participant %q", participantID)
		}

		seenParticipants[participantID] = struct{}{}
	}

	for _, artifact := range t.Evidence {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("invalid evidence: %w", err)
		}
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
