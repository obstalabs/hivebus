package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type CapabilityAttestationState string

const (
	CapabilityAttestationRequested    CapabilityAttestationState = "requested"
	CapabilityAttestationVerified     CapabilityAttestationState = "verified"
	CapabilityAttestationDoctorPassed CapabilityAttestationState = "doctor_passed"
	CapabilityAttestationDoctorFailed CapabilityAttestationState = "doctor_failed"
	CapabilityAttestationActive       CapabilityAttestationState = "active"
)

var validCapabilityAttestationStates = []CapabilityAttestationState{
	CapabilityAttestationRequested,
	CapabilityAttestationVerified,
	CapabilityAttestationDoctorPassed,
	CapabilityAttestationDoctorFailed,
	CapabilityAttestationActive,
}

type CapabilityTaskOutcome string

const (
	CapabilityTaskSucceeded CapabilityTaskOutcome = "succeeded"
	CapabilityTaskFailed    CapabilityTaskOutcome = "failed"
)

var validCapabilityTaskOutcomes = []CapabilityTaskOutcome{
	CapabilityTaskSucceeded,
	CapabilityTaskFailed,
}

type CapabilityTeardownState string

const (
	CapabilityTeardownRequested CapabilityTeardownState = "requested"
	CapabilityTeardownCompleted CapabilityTeardownState = "completed"
	CapabilityTeardownFailed    CapabilityTeardownState = "failed"
)

var validCapabilityTeardownStates = []CapabilityTeardownState{
	CapabilityTeardownRequested,
	CapabilityTeardownCompleted,
	CapabilityTeardownFailed,
}

// CapabilityLifecyclePayload records the append-only provenance needed to prove
// a temporary capability's install, verification, use, and teardown lifecycle.
type CapabilityLifecyclePayload struct {
	Host              string                     `json:"host"`
	CapabilityID      string                     `json:"capability_id"`
	CapabilityClass   string                     `json:"capability_class"`
	Version           string                     `json:"version"`
	Digest            string                     `json:"digest"`
	Signer            string                     `json:"signer"`
	RequestedBy       string                     `json:"requested_by"`
	ApprovedBy        string                     `json:"approved_by,omitempty"`
	OriginThreadID    string                     `json:"origin_thread_id,omitempty"`
	OriginWorkOrderID string                     `json:"origin_work_order_id,omitempty"`
	EvidenceIDs       []string                   `json:"evidence_ids,omitempty"`
	AttestationState  CapabilityAttestationState `json:"attestation_state,omitempty"`
	TaskOutcome       CapabilityTaskOutcome      `json:"task_outcome,omitempty"`
	TeardownState     CapabilityTeardownState    `json:"teardown_state,omitempty"`
	FailureReason     string                     `json:"failure_reason,omitempty"`
}

func (p CapabilityLifecyclePayload) Validate(eventType MessageType) error {
	switch {
	case strings.TrimSpace(p.Host) == "":
		return errors.New("host is required")
	case strings.TrimSpace(p.CapabilityID) == "":
		return errors.New("capability_id is required")
	case strings.TrimSpace(p.CapabilityClass) == "":
		return errors.New("capability_class is required")
	case strings.TrimSpace(p.Version) == "":
		return errors.New("version is required")
	case strings.TrimSpace(p.Digest) == "":
		return errors.New("digest is required")
	case !IsSHA256Hex(strings.TrimSpace(p.Digest)):
		return errors.New("digest must be a 64-character lowercase hex digest")
	case strings.TrimSpace(p.Signer) == "":
		return errors.New("signer is required")
	case strings.TrimSpace(p.RequestedBy) == "":
		return errors.New("requested_by is required")
	case strings.TrimSpace(p.OriginThreadID) == "" && strings.TrimSpace(p.OriginWorkOrderID) == "":
		return errors.New("either origin_thread_id or origin_work_order_id is required")
	}

	seenEvidence := make(map[string]struct{}, len(p.EvidenceIDs))
	for _, evidenceID := range p.EvidenceIDs {
		evidenceID = strings.TrimSpace(evidenceID)
		if evidenceID == "" {
			return errors.New("evidence_ids contains an empty artifact id")
		}
		if _, exists := seenEvidence[evidenceID]; exists {
			return fmt.Errorf("duplicate evidence id %q", evidenceID)
		}
		seenEvidence[evidenceID] = struct{}{}
	}

	if p.AttestationState != "" && !slices.Contains(validCapabilityAttestationStates, p.AttestationState) {
		return fmt.Errorf("unsupported attestation_state %q", p.AttestationState)
	}
	if p.TaskOutcome != "" && !slices.Contains(validCapabilityTaskOutcomes, p.TaskOutcome) {
		return fmt.Errorf("unsupported task_outcome %q", p.TaskOutcome)
	}
	if p.TeardownState != "" && !slices.Contains(validCapabilityTeardownStates, p.TeardownState) {
		return fmt.Errorf("unsupported teardown_state %q", p.TeardownState)
	}

	switch eventType {
	case MessageTypeInstallRequested:
		return validateLifecycleState(
			p,
			CapabilityAttestationRequested,
			"",
			"",
			false,
			true,
		)
	case MessageTypeInstallVerified:
		return validateLifecycleState(
			p,
			CapabilityAttestationVerified,
			"",
			"",
			true,
			true,
		)
	case MessageTypeDoctorPassed:
		return validateLifecycleState(
			p,
			CapabilityAttestationDoctorPassed,
			"",
			"",
			true,
			true,
		)
	case MessageTypeDoctorFailed:
		return validateLifecycleState(
			p,
			CapabilityAttestationDoctorFailed,
			"",
			"",
			true,
			false,
		)
	case MessageTypeCapabilityActive:
		return validateLifecycleState(
			p,
			CapabilityAttestationActive,
			"",
			"",
			true,
			true,
		)
	case MessageTypeTaskCompleted:
		if p.AttestationState != CapabilityAttestationActive {
			return errors.New("capability.task.completed requires attestation_state active")
		}
		if p.TaskOutcome == "" {
			return errors.New("capability.task.completed requires task_outcome")
		}
		if p.TeardownState != "" {
			return errors.New("capability.task.completed must not set teardown_state")
		}
		if strings.TrimSpace(p.ApprovedBy) == "" {
			return errors.New("approved_by is required")
		}
		if p.TaskOutcome == CapabilityTaskFailed && strings.TrimSpace(p.FailureReason) == "" {
			return errors.New("capability.task.completed requires failure_reason when task_outcome is failed")
		}
		if p.TaskOutcome == CapabilityTaskSucceeded && strings.TrimSpace(p.FailureReason) != "" {
			return errors.New("capability.task.completed must not set failure_reason when task_outcome succeeded")
		}
		return nil
	case MessageTypeTeardownRequested:
		return validateLifecycleState(
			p,
			CapabilityAttestationActive,
			"",
			CapabilityTeardownRequested,
			true,
			true,
		)
	case MessageTypeTeardownCompleted:
		return validateLifecycleState(
			p,
			CapabilityAttestationActive,
			"",
			CapabilityTeardownCompleted,
			true,
			true,
		)
	case MessageTypeTeardownFailed:
		return validateLifecycleState(
			p,
			CapabilityAttestationActive,
			"",
			CapabilityTeardownFailed,
			true,
			false,
		)
	default:
		return fmt.Errorf("unsupported capability lifecycle event %q", eventType)
	}
}

func (e Envelope) ValidateCapabilityLifecycle() error {
	if err := e.Validate(); err != nil {
		return err
	}

	switch e.Type {
	case MessageTypeInstallRequested,
		MessageTypeInstallVerified,
		MessageTypeDoctorPassed,
		MessageTypeDoctorFailed,
		MessageTypeCapabilityActive,
		MessageTypeTaskCompleted,
		MessageTypeTeardownRequested,
		MessageTypeTeardownCompleted,
		MessageTypeTeardownFailed:
	default:
		return fmt.Errorf("expected capability lifecycle event, got %q", e.Type)
	}

	var payload CapabilityLifecyclePayload
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		return fmt.Errorf("decode capability lifecycle payload: %w", err)
	}

	return payload.Validate(e.Type)
}

func validateLifecycleState(
	payload CapabilityLifecyclePayload,
	attestation CapabilityAttestationState,
	taskOutcome CapabilityTaskOutcome,
	teardown CapabilityTeardownState,
	requireApprover bool,
	allowEmptyFailure bool,
) error {
	if payload.AttestationState != attestation {
		return fmt.Errorf("expected attestation_state %q, got %q", attestation, payload.AttestationState)
	}
	if payload.TaskOutcome != taskOutcome {
		if taskOutcome == "" {
			return errors.New("task_outcome must be empty")
		}
		return fmt.Errorf("expected task_outcome %q, got %q", taskOutcome, payload.TaskOutcome)
	}
	if payload.TeardownState != teardown {
		if teardown == "" {
			return errors.New("teardown_state must be empty")
		}
		return fmt.Errorf("expected teardown_state %q, got %q", teardown, payload.TeardownState)
	}
	if requireApprover && strings.TrimSpace(payload.ApprovedBy) == "" {
		return errors.New("approved_by is required")
	}
	if !requireApprover && strings.TrimSpace(payload.ApprovedBy) != "" {
		return errors.New("approved_by must be empty")
	}
	if allowEmptyFailure {
		if strings.TrimSpace(payload.FailureReason) != "" {
			return errors.New("failure_reason must be empty")
		}
		return nil
	}
	if strings.TrimSpace(payload.FailureReason) == "" {
		return errors.New("failure_reason is required")
	}

	return nil
}
