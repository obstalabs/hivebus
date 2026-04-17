package model

import "testing"

func TestAuthorizationContextValidateAcceptsApprovedClarification(t *testing.T) {
	t.Helper()

	context := sampleAuthorizationContext()
	context.RequestClass = AuthorizationRequestClarification
	context.RequestedScope = "thread.reply"
	context.ApprovalState = AuthorizationApproved

	if err := context.Validate(AuthorizationRequestClarification); err != nil {
		t.Fatalf("Validate(clarification) error = %v", err)
	}
}

func TestAuthorizationContextValidateRejectsDeniedWithoutReason(t *testing.T) {
	t.Helper()

	context := sampleAuthorizationContext()
	context.ApprovalState = AuthorizationDenied
	context.RefusalReason = ""

	if err := context.Validate(AuthorizationRequestCapability); err == nil {
		t.Fatal("Validate(capability) expected an error")
	}
}

func TestClarificationRequestPayloadValidateAcceptsStructuredAuthorization(t *testing.T) {
	t.Helper()

	payload := ClarificationRequestPayload{
		Question: "Did the incident start after the 07:30 deployment?",
		Round:    1,
		Authorization: AuthorizationContext{
			SenderParticipantID:   "agent.field.nullbot",
			ParticipantMembership: ParticipantMembershipThreadParticipant,
			RequestedScope:        "thread.reply",
			RequestClass:          AuthorizationRequestClarification,
			ApprovalState:         AuthorizationNotRequired,
			ExpiresAt:             "2026-04-17T09:00:00Z",
		},
	}

	if err := payload.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func sampleAuthorizationContext() AuthorizationContext {
	return AuthorizationContext{
		SenderParticipantID:   "collector.nullbot",
		ParticipantMembership: ParticipantMembershipThreadParticipant,
		RequestedScope:        "capability.install.go-testing",
		RequestClass:          AuthorizationRequestCapability,
		ApprovalState:         AuthorizationApproved,
		ExpiresAt:             "2026-04-18T06:00:00Z",
	}
}
