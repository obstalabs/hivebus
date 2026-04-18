package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type CapabilityTrustLevel string

const (
	CapabilityTrustObserved  CapabilityTrustLevel = "observed"
	CapabilityTrustValidated CapabilityTrustLevel = "validated"
	CapabilityTrustTrusted   CapabilityTrustLevel = "trusted"
)

var validCapabilityTrustLevels = []CapabilityTrustLevel{
	CapabilityTrustObserved,
	CapabilityTrustValidated,
	CapabilityTrustTrusted,
}

type CapabilityRiskLevel string

const (
	CapabilityRiskLow    CapabilityRiskLevel = "low"
	CapabilityRiskMedium CapabilityRiskLevel = "medium"
	CapabilityRiskHigh   CapabilityRiskLevel = "high"
)

var validCapabilityRiskLevels = []CapabilityRiskLevel{
	CapabilityRiskLow,
	CapabilityRiskMedium,
	CapabilityRiskHigh,
}

// TaskResultFinalPayload is the optional structured payload Hivebus can inspect
// when deriving capability observations from a completed lease result.
type TaskResultFinalPayload struct {
	Status    string   `json:"status,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	ToolsUsed []string `json:"tools_used,omitempty"`
}

// DiscoveredCapability describes the operator-facing state of one inferred
// worker capability derived from completed work.
type DiscoveredCapability struct {
	WorkerID         string               `json:"worker_id"`
	CapabilityID     string               `json:"capability_id"`
	Requirements     []string             `json:"requirements,omitempty"`
	RiskLevel        CapabilityRiskLevel  `json:"risk_level"`
	TrustLevel       CapabilityTrustLevel `json:"trust_level"`
	ObservationCount int                  `json:"observation_count"`
	SuccessCount     int                  `json:"success_count"`
	FirstObservedAt  time.Time            `json:"first_observed_at"`
	LastObservedAt   time.Time            `json:"last_observed_at"`
	ApprovedBy       string               `json:"approved_by,omitempty"`
	ApprovedAt       *time.Time           `json:"approved_at,omitempty"`
	PendingApproval  bool                 `json:"pending_approval"`
	Decayed          bool                 `json:"decayed"`
	LastObservedTool string               `json:"last_observed_tool,omitempty"`
}

func (p TaskResultFinalPayload) NormalizedTools() []string {
	seen := make(map[string]struct{}, len(p.ToolsUsed))
	normalized := make([]string, 0, len(p.ToolsUsed))
	for _, tool := range p.ToolsUsed {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, exists := seen[tool]; exists {
			continue
		}
		seen[tool] = struct{}{}
		normalized = append(normalized, tool)
	}
	return normalized
}

func (p TaskResultFinalPayload) Successful() bool {
	switch strings.ToLower(strings.TrimSpace(p.Status)) {
	case "", "done", "success", "succeeded", "completed", "ok":
		return true
	case "failed", "failure", "error":
		return false
	default:
		return true
	}
}

func ParseTaskResultFinalPayload(raw json.RawMessage) (TaskResultFinalPayload, error) {
	if len(raw) == 0 {
		return TaskResultFinalPayload{}, nil
	}

	var payload TaskResultFinalPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return TaskResultFinalPayload{}, err
	}

	return payload, nil
}

func (c DiscoveredCapability) Validate() error {
	switch {
	case strings.TrimSpace(c.WorkerID) == "":
		return errors.New("worker_id is required")
	case strings.TrimSpace(c.CapabilityID) == "":
		return errors.New("capability_id is required")
	case !slices.Contains(validCapabilityRiskLevels, c.RiskLevel):
		return fmt.Errorf("unsupported risk_level %q", c.RiskLevel)
	case !slices.Contains(validCapabilityTrustLevels, c.TrustLevel):
		return fmt.Errorf("unsupported trust_level %q", c.TrustLevel)
	case c.ObservationCount < 0:
		return errors.New("observation_count must be zero or positive")
	case c.SuccessCount < 0:
		return errors.New("success_count must be zero or positive")
	case c.SuccessCount > c.ObservationCount:
		return errors.New("success_count must not exceed observation_count")
	case c.FirstObservedAt.IsZero():
		return errors.New("first_observed_at is required")
	case c.LastObservedAt.IsZero():
		return errors.New("last_observed_at is required")
	case c.LastObservedAt.Before(c.FirstObservedAt):
		return errors.New("last_observed_at must not be before first_observed_at")
	}

	seen := make(map[string]struct{}, len(c.Requirements))
	for _, requirement := range c.Requirements {
		requirement = strings.TrimSpace(requirement)
		if requirement == "" {
			return errors.New("requirements contains an empty requirement")
		}
		if _, exists := seen[requirement]; exists {
			return fmt.Errorf("duplicate requirement %q", requirement)
		}
		seen[requirement] = struct{}{}
	}

	if c.TrustLevel == CapabilityTrustTrusted {
		if strings.TrimSpace(c.ApprovedBy) == "" {
			return errors.New("trusted capability requires approved_by")
		}
		if c.ApprovedAt == nil || c.ApprovedAt.IsZero() {
			return errors.New("trusted capability requires approved_at")
		}
	}

	if c.TrustLevel != CapabilityTrustTrusted {
		if strings.TrimSpace(c.ApprovedBy) != "" {
			return errors.New("non-trusted capability must not set approved_by")
		}
		if c.ApprovedAt != nil && !c.ApprovedAt.IsZero() {
			return errors.New("non-trusted capability must not set approved_at")
		}
	}

	return nil
}
