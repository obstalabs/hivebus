package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type AuthorizationRequestClass string

const (
	AuthorizationRequestClarification AuthorizationRequestClass = "clarification"
	AuthorizationRequestCapability    AuthorizationRequestClass = "capability"
	AuthorizationRequestEvidence      AuthorizationRequestClass = "evidence"
)

var validAuthorizationRequestClasses = []AuthorizationRequestClass{
	AuthorizationRequestClarification,
	AuthorizationRequestCapability,
	AuthorizationRequestEvidence,
}

type AuthorizationApprovalState string

const (
	AuthorizationNotRequired AuthorizationApprovalState = "not_required"
	AuthorizationPending     AuthorizationApprovalState = "pending"
	AuthorizationApproved    AuthorizationApprovalState = "approved"
	AuthorizationDenied      AuthorizationApprovalState = "denied"
)

var validAuthorizationApprovalStates = []AuthorizationApprovalState{
	AuthorizationNotRequired,
	AuthorizationPending,
	AuthorizationApproved,
	AuthorizationDenied,
}

type AuthorizationRefusalReason string

const (
	AuthorizationUnauthorizedSender  AuthorizationRefusalReason = "unauthorized_sender"
	AuthorizationScopeMismatch       AuthorizationRefusalReason = "scope_mismatch"
	AuthorizationExpiredRequest      AuthorizationRefusalReason = "expired_request"
	AuthorizationForbiddenCapability AuthorizationRefusalReason = "forbidden_capability"
	AuthorizationMissingApproval     AuthorizationRefusalReason = "missing_approval"
)

var validAuthorizationRefusalReasons = []AuthorizationRefusalReason{
	AuthorizationUnauthorizedSender,
	AuthorizationScopeMismatch,
	AuthorizationExpiredRequest,
	AuthorizationForbiddenCapability,
	AuthorizationMissingApproval,
}

type ParticipantMembershipState string

const (
	ParticipantMembershipThreadParticipant  ParticipantMembershipState = "thread_participant"
	ParticipantMembershipServiceParticipant ParticipantMembershipState = "service_participant"
	ParticipantMembershipNonParticipant     ParticipantMembershipState = "non_participant"
)

var validParticipantMembershipStates = []ParticipantMembershipState{
	ParticipantMembershipThreadParticipant,
	ParticipantMembershipServiceParticipant,
	ParticipantMembershipNonParticipant,
}

// AuthorizationContext makes sender identity, membership, scope, class, expiry,
// and approval state explicit so field agents never have to guess from prose.
type AuthorizationContext struct {
	SenderParticipantID   string                     `json:"sender_participant_id"`
	ParticipantMembership ParticipantMembershipState `json:"participant_membership"`
	RequestedScope        string                     `json:"requested_scope"`
	RequestClass          AuthorizationRequestClass  `json:"request_class"`
	ApprovalState         AuthorizationApprovalState `json:"approval_state"`
	ExpiresAt             string                     `json:"expires_at,omitempty"`
	RefusalReason         AuthorizationRefusalReason `json:"refusal_reason,omitempty"`
}

func (c AuthorizationContext) Validate(expectedClass AuthorizationRequestClass) error {
	switch {
	case strings.TrimSpace(c.SenderParticipantID) == "":
		return errors.New("sender_participant_id is required")
	case !slices.Contains(validParticipantMembershipStates, c.ParticipantMembership):
		return fmt.Errorf("unsupported participant_membership %q", c.ParticipantMembership)
	case strings.TrimSpace(c.RequestedScope) == "":
		return errors.New("requested_scope is required")
	case !slices.Contains(validAuthorizationRequestClasses, c.RequestClass):
		return fmt.Errorf("unsupported request_class %q", c.RequestClass)
	case c.RequestClass != expectedClass:
		return fmt.Errorf("expected request_class %q, got %q", expectedClass, c.RequestClass)
	case !slices.Contains(validAuthorizationApprovalStates, c.ApprovalState):
		return fmt.Errorf("unsupported approval_state %q", c.ApprovalState)
	}

	if c.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, c.ExpiresAt); err != nil {
			return fmt.Errorf("expires_at must be RFC3339: %w", err)
		}
	}

	if c.RefusalReason != "" && !slices.Contains(validAuthorizationRefusalReasons, c.RefusalReason) {
		return fmt.Errorf("unsupported refusal_reason %q", c.RefusalReason)
	}

	switch c.ApprovalState {
	case AuthorizationDenied:
		if c.RefusalReason == "" {
			return errors.New("denied authorization requires refusal_reason")
		}
	case AuthorizationApproved, AuthorizationPending, AuthorizationNotRequired:
		if c.RefusalReason != "" {
			return fmt.Errorf("%s authorization must not set refusal_reason", c.ApprovalState)
		}
	}

	if c.ParticipantMembership == ParticipantMembershipNonParticipant &&
		c.ApprovalState != AuthorizationDenied {
		return errors.New("non_participant authorization must be denied")
	}

	return nil
}

type ClarificationRequestPayload struct {
	Question      string               `json:"question"`
	Authorization AuthorizationContext `json:"authorization"`
}

func (p ClarificationRequestPayload) Validate() error {
	if strings.TrimSpace(p.Question) == "" {
		return errors.New("question is required")
	}
	return p.Authorization.Validate(AuthorizationRequestClarification)
}
