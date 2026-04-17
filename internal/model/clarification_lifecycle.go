package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type ClarificationReceiptState string

const (
	ClarificationReceiptQueued      ClarificationReceiptState = "queued"
	ClarificationReceiptDelivered   ClarificationReceiptState = "delivered"
	ClarificationReceiptUnanswered  ClarificationReceiptState = "unanswered"
	ClarificationReceiptRefused     ClarificationReceiptState = "refused"
	ClarificationReceiptDuplicate   ClarificationReceiptState = "duplicate_delivery"
	ClarificationReceiptStale       ClarificationReceiptState = "stale_request"
	ClarificationReceiptSessionSwap ClarificationReceiptState = "session_replaced"
)

var validClarificationReceiptStates = []ClarificationReceiptState{
	ClarificationReceiptQueued,
	ClarificationReceiptDelivered,
	ClarificationReceiptUnanswered,
	ClarificationReceiptRefused,
	ClarificationReceiptDuplicate,
	ClarificationReceiptStale,
	ClarificationReceiptSessionSwap,
}

type ClarificationFailureState string

const (
	ClarificationFailureDuplicateRequest ClarificationFailureState = "duplicate_request"
	ClarificationFailureStaleRequest     ClarificationFailureState = "stale_request"
	ClarificationFailureStaleResponse    ClarificationFailureState = "stale_response"
	ClarificationFailureConflicting      ClarificationFailureState = "conflicting_request"
	ClarificationFailureExpired          ClarificationFailureState = "expired_request"
	ClarificationFailureThreadFinalized  ClarificationFailureState = "thread_finalized"
	ClarificationFailureSessionReplaced  ClarificationFailureState = "session_replaced"
	ClarificationFailureMaxRounds        ClarificationFailureState = "max_rounds_exceeded"
)

var validClarificationFailureStates = []ClarificationFailureState{
	ClarificationFailureDuplicateRequest,
	ClarificationFailureStaleRequest,
	ClarificationFailureStaleResponse,
	ClarificationFailureConflicting,
	ClarificationFailureExpired,
	ClarificationFailureThreadFinalized,
	ClarificationFailureSessionReplaced,
	ClarificationFailureMaxRounds,
}

type ClarificationTerminalOutcome string

const (
	ClarificationOutcomeReadyForWO ClarificationTerminalOutcome = "ready_for_wo"
	ClarificationOutcomeNeedsHuman ClarificationTerminalOutcome = "needs_human"
	ClarificationOutcomeAbandoned  ClarificationTerminalOutcome = "abandoned"
)

var validClarificationTerminalOutcomes = []ClarificationTerminalOutcome{
	ClarificationOutcomeReadyForWO,
	ClarificationOutcomeNeedsHuman,
	ClarificationOutcomeAbandoned,
}

type ClarificationReceiptPayload struct {
	RequestMessageID string                    `json:"request_message_id"`
	TargetSessionID  string                    `json:"target_session_id,omitempty"`
	State            ClarificationReceiptState `json:"state"`
	QueuePosition    int                       `json:"queue_position,omitempty"`
	Reason           string                    `json:"reason,omitempty"`
}

func (p ClarificationReceiptPayload) Validate() error {
	switch {
	case strings.TrimSpace(p.RequestMessageID) == "":
		return errors.New("request_message_id is required")
	case !slices.Contains(validClarificationReceiptStates, p.State):
		return fmt.Errorf("unsupported state %q", p.State)
	}

	switch p.State {
	case ClarificationReceiptQueued:
		if p.QueuePosition <= 0 {
			return errors.New("queued clarification receipt requires positive queue_position")
		}
		if strings.TrimSpace(p.TargetSessionID) != "" {
			return errors.New("queued clarification receipt must not set target_session_id")
		}
	case ClarificationReceiptDelivered:
		if strings.TrimSpace(p.TargetSessionID) == "" {
			return errors.New("delivered clarification receipt requires target_session_id")
		}
		if p.QueuePosition != 0 {
			return errors.New("delivered clarification receipt must not set queue_position")
		}
	case ClarificationReceiptUnanswered,
		ClarificationReceiptRefused,
		ClarificationReceiptDuplicate,
		ClarificationReceiptStale,
		ClarificationReceiptSessionSwap:
		if strings.TrimSpace(p.Reason) == "" {
			return fmt.Errorf("%s clarification receipt requires reason", p.State)
		}
	default:
		return fmt.Errorf("unsupported state %q", p.State)
	}

	return nil
}

type ClarificationOutcomePayload struct {
	RequestMessageID string                       `json:"request_message_id"`
	Outcome          ClarificationTerminalOutcome `json:"outcome"`
	FailureState     ClarificationFailureState    `json:"failure_state,omitempty"`
	MaxRounds        int                          `json:"max_rounds"`
	MaxEvidenceBytes int64                        `json:"max_evidence_bytes"`
	RoundsUsed       int                          `json:"rounds_used"`
	EvidenceBytes    int64                        `json:"evidence_bytes"`
}

func (p ClarificationOutcomePayload) Validate() error {
	switch {
	case strings.TrimSpace(p.RequestMessageID) == "":
		return errors.New("request_message_id is required")
	case !slices.Contains(validClarificationTerminalOutcomes, p.Outcome):
		return fmt.Errorf("unsupported outcome %q", p.Outcome)
	case p.MaxRounds <= 0:
		return errors.New("max_rounds must be positive")
	case p.MaxEvidenceBytes <= 0:
		return errors.New("max_evidence_bytes must be positive")
	case p.RoundsUsed <= 0:
		return errors.New("rounds_used must be positive")
	case p.RoundsUsed > p.MaxRounds:
		return errors.New("rounds_used must not exceed max_rounds")
	case p.EvidenceBytes < 0:
		return errors.New("evidence_bytes must be zero or positive")
	case p.EvidenceBytes > p.MaxEvidenceBytes:
		return errors.New("evidence_bytes must not exceed max_evidence_bytes")
	}

	if p.FailureState != "" && !slices.Contains(validClarificationFailureStates, p.FailureState) {
		return fmt.Errorf("unsupported failure_state %q", p.FailureState)
	}

	switch p.Outcome {
	case ClarificationOutcomeReadyForWO:
		if p.FailureState != "" {
			return errors.New("ready_for_wo clarification outcome must not set failure_state")
		}
	case ClarificationOutcomeNeedsHuman, ClarificationOutcomeAbandoned:
		if p.FailureState == "" {
			return fmt.Errorf("%s clarification outcome requires failure_state", p.Outcome)
		}
	default:
		return fmt.Errorf("unsupported outcome %q", p.Outcome)
	}

	return nil
}

func (e Envelope) ValidateClarificationLifecycle() error {
	if err := e.Validate(); err != nil {
		return err
	}

	switch e.Type {
	case MessageTypeClarifyReceipt:
		var payload ClarificationReceiptPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("decode clarification receipt payload: %w", err)
		}
		return payload.Validate()
	case MessageTypeClarifyOutcome:
		var payload ClarificationOutcomePayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("decode clarification outcome payload: %w", err)
		}
		return payload.Validate()
	default:
		return fmt.Errorf("expected clarification lifecycle event, got %q", e.Type)
	}
}
