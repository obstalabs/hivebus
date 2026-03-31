package work

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ppiankov/hivebus/internal/model"
)

// Draft is the workledger-tracked unit created only after a verified diagnosis.
type Draft struct {
	TrackingSystem      string           `json:"tracking_system"`
	OptionalSyncTargets []string         `json:"optional_sync_targets,omitempty"`
	Title               string           `json:"title"`
	Priority            string           `json:"priority"`
	Summary             string           `json:"summary"`
	Scope               []string         `json:"scope"`
	AcceptanceCriteria  []string         `json:"acceptance_criteria"`
	EvidenceIDs         []string         `json:"evidence_ids"`
	SourceThreadID      string           `json:"source_thread_id"`
	CustomerTier        model.Tier       `json:"customer_tier"`
	Confidence          model.Confidence `json:"confidence"`
}

// DraftFromThread refuses to generate a WO until the diagnostic thread is structurally ready.
func DraftFromThread(thread model.Thread, diagnosis model.Diagnosis) (Draft, error) {
	if err := thread.Validate(); err != nil {
		return Draft{}, err
	}

	if err := diagnosis.Validate(); err != nil {
		return Draft{}, err
	}

	if thread.Status != model.ThreadStatusReadyForWork && thread.Status != model.ThreadStatusDone {
		return Draft{}, fmt.Errorf("thread %s is not ready for work-order creation", thread.Status)
	}

	if !diagnosis.Verified {
		return Draft{}, errors.New("diagnosis must be verified before work-order creation")
	}

	if len(diagnosis.MissingInfo) > 0 {
		return Draft{}, errors.New("diagnosis still has missing information")
	}

	return Draft{
		TrackingSystem: "workledger",
		Title:          "Resolve: " + strings.TrimSpace(thread.Title),
		Priority:       priorityForTier(thread.CustomerTier),
		Summary:        buildSummary(thread, diagnosis),
		Scope:          append([]string(nil), diagnosis.ProposedRemediation...),
		AcceptanceCriteria: []string{
			"Original symptom no longer reproduces in the affected environment.",
			"Evidence from thread " + thread.ThreadID + " is linked to the remediation record.",
			"Operational owner confirms the user story is fully resolved.",
		},
		EvidenceIDs:    append([]string(nil), diagnosis.EvidenceIDs...),
		SourceThreadID: thread.ThreadID,
		CustomerTier:   thread.CustomerTier,
		Confidence:     diagnosis.Confidence,
	}, nil
}

func buildSummary(thread model.Thread, diagnosis model.Diagnosis) string {
	return fmt.Sprintf(
		"%s\n\nLikely cause: %s\nSource: %s\nTier: %s",
		diagnosis.Problem,
		diagnosis.LikelyCause,
		thread.Source,
		thread.CustomerTier,
	)
}

func priorityForTier(tier model.Tier) string {
	switch tier {
	case model.TierEnterprise, model.TierTeams:
		return "P1"
	case model.TierPro:
		return "P2"
	default:
		return "P3"
	}
}
