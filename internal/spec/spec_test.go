package spec

import (
	"encoding/json"
	"testing"

	"github.com/ppiankov/hivebus/internal/model"
)

func TestSampleCaseBuildsWorkOrderForSameThread(t *testing.T) {
	t.Helper()

	sample := SampleCase()

	if sample.WorkOrder.SourceThreadID != sample.Thread.ThreadID {
		t.Fatalf("expected work order source thread %q, got %q", sample.Thread.ThreadID, sample.WorkOrder.SourceThreadID)
	}

	if len(sample.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(sample.Messages))
	}
}

func TestV0DeclaresWorkledgerAsTrackingSystem(t *testing.T) {
	t.Helper()

	document := V0()

	if document.TrackingSystem != "workledger" {
		t.Fatalf("expected tracking system workledger, got %q", document.TrackingSystem)
	}

	if document.OptionalBridge != "hiveram.com" {
		t.Fatalf("expected optional bridge hiveram.com, got %q", document.OptionalBridge)
	}

	if document.EditionBoundary.RepoByTier["free"] != "hivebus" {
		t.Fatalf("expected free tier in hivebus, got %q", document.EditionBoundary.RepoByTier["free"])
	}

	if document.EditionBoundary.RepoByTier["pro"] != "hivebus-pro" {
		t.Fatalf("expected pro tier in hivebus-pro, got %q", document.EditionBoundary.RepoByTier["pro"])
	}

	if len(document.FeatureBoundary.CommunityFeatures) == 0 {
		t.Fatal("expected community feature boundary")
	}

	if len(document.ArtifactManifestFields) != 3 {
		t.Fatalf("expected artifact manifest fields, got %#v", document.ArtifactManifestFields)
	}

	if document.FeatureBoundary.Workledger.ProjectSelection == "" {
		t.Fatal("expected workledger project selection strategy")
	}

	if !document.WorkOrderGate["workledger_project_required"] {
		t.Fatal("expected workledger project gate")
	}

	if len(document.Participants.Types) != 3 {
		t.Fatalf("expected 3 participant types, got %d", len(document.Participants.Types))
	}

	if len(document.Participants.SharedFields) == 0 {
		t.Fatal("expected participant shared fields")
	}

	if len(document.Participants.TypeSpecificFields["human"]) == 0 {
		t.Fatal("expected human participant fields")
	}

	if len(document.Participants.TypeSpecificFields["agent"]) == 0 {
		t.Fatal("expected agent participant fields")
	}

	if len(document.Participants.TypeSpecificFields["service"]) == 0 {
		t.Fatal("expected service participant fields")
	}

	if len(document.Participants.MembershipRules["human"]) == 0 {
		t.Fatal("expected human membership rules")
	}

	if len(document.Participants.VisibilityRules["service"]) == 0 {
		t.Fatal("expected service visibility rules")
	}

	if len(document.Participants.AuthorizationRules["agent"]) == 0 {
		t.Fatal("expected agent authorization rules")
	}

	if len(document.Authorization.RequiredFields) == 0 {
		t.Fatal("expected authorization required fields")
	}

	if len(document.Authorization.RequestClasses) != 3 {
		t.Fatalf("expected 3 authorization request classes, got %d", len(document.Authorization.RequestClasses))
	}

	if len(document.Authorization.ApprovalStates) != 4 {
		t.Fatalf("expected 4 authorization approval states, got %d", len(document.Authorization.ApprovalStates))
	}

	if len(document.Authorization.RefusalReasons) != 5 {
		t.Fatalf("expected 5 authorization refusal reasons, got %d", len(document.Authorization.RefusalReasons))
	}

	if len(document.Authorization.MembershipStates) != 3 {
		t.Fatalf("expected 3 authorization membership states, got %d", len(document.Authorization.MembershipStates))
	}

	if len(document.Authorization.DecisionPoints) == 0 {
		t.Fatal("expected authorization decision points")
	}

	if len(document.Authorization.Consumers["field_agent"]) == 0 {
		t.Fatal("expected field agent authorization rules")
	}

	if len(document.ClarificationLifecycle.ReceiptStates) != 7 {
		t.Fatalf("expected 7 clarification receipt states, got %d", len(document.ClarificationLifecycle.ReceiptStates))
	}

	if len(document.ClarificationLifecycle.FailureStates) != 8 {
		t.Fatalf("expected 8 clarification failure states, got %d", len(document.ClarificationLifecycle.FailureStates))
	}

	if len(document.ClarificationLifecycle.TerminalOutcomes) != 3 {
		t.Fatalf("expected 3 clarification terminal outcomes, got %d", len(document.ClarificationLifecycle.TerminalOutcomes))
	}

	if len(document.ClarificationLifecycle.BudgetFields) != 4 {
		t.Fatalf("expected 4 clarification budget fields, got %d", len(document.ClarificationLifecycle.BudgetFields))
	}

	if len(document.ClarificationLifecycle.Consumers["sentinel"]) == 0 {
		t.Fatal("expected clarification lifecycle sentinel rules")
	}

	if len(document.CapabilityLifecycle.MessageTypes) != 9 {
		t.Fatalf("expected 9 capability lifecycle message types, got %d", len(document.CapabilityLifecycle.MessageTypes))
	}

	if len(document.CapabilityLifecycle.RequiredFields) == 0 {
		t.Fatal("expected capability lifecycle required fields")
	}

	if len(document.CapabilityLifecycle.DeliveryModes) != 2 {
		t.Fatalf("expected 2 capability delivery modes, got %d", len(document.CapabilityLifecycle.DeliveryModes))
	}

	if len(document.CapabilityLifecycle.RefusalReasons) != 4 {
		t.Fatalf("expected 4 capability refusal reasons, got %d", len(document.CapabilityLifecycle.RefusalReasons))
	}

	if len(document.CapabilityLifecycle.TrustRoots) == 0 {
		t.Fatal("expected capability lifecycle trust roots")
	}

	if len(document.CapabilityLifecycle.Consumers["sentinel"]) == 0 {
		t.Fatal("expected sentinel capability lifecycle consumer rules")
	}

	if len(document.CapabilityLifecycle.Consumers["workledger"]) == 0 {
		t.Fatal("expected workledger capability lifecycle consumer rules")
	}

	if len(document.CapabilityLifecycle.Consumers["policy"]) == 0 {
		t.Fatal("expected policy capability lifecycle consumer rules")
	}

	if len(document.EdgeRouting.MessageTypes) != 3 {
		t.Fatalf("expected 3 edge routing message types, got %d", len(document.EdgeRouting.MessageTypes))
	}

	if len(document.EdgeRouting.SessionFields) == 0 {
		t.Fatal("expected edge routing session fields")
	}

	if len(document.EdgeRouting.ReceiptFields) == 0 {
		t.Fatal("expected edge routing receipt fields")
	}

	if len(document.EdgeRouting.DeliveryModes) != 2 {
		t.Fatalf("expected 2 edge routing delivery modes, got %d", len(document.EdgeRouting.DeliveryModes))
	}

	if len(document.EdgeRouting.SessionStates) != 3 {
		t.Fatalf("expected 3 edge routing session states, got %d", len(document.EdgeRouting.SessionStates))
	}

	if len(document.EdgeRouting.ReceiptStates) != 4 {
		t.Fatalf("expected 4 edge routing receipt states, got %d", len(document.EdgeRouting.ReceiptStates))
	}

	if document.EdgeRouting.RoutingRule == "" {
		t.Fatal("expected edge routing rule")
	}

	if len(document.EdgeRouting.Consumers["hivebus"]) == 0 {
		t.Fatal("expected hivebus edge routing consumer rules")
	}
}

func TestSampleCapabilityLifecycleValidatesAllEvents(t *testing.T) {
	t.Helper()

	sample := SampleCapabilityLifecycle()
	if len(sample.Messages) != 7 {
		t.Fatalf("expected 7 capability lifecycle messages, got %d", len(sample.Messages))
	}

	for _, envelope := range sample.Messages {
		if err := envelope.ValidateCapabilityLifecycle(); err != nil {
			t.Fatalf("ValidateCapabilityLifecycle(%s) error = %v", envelope.Type, err)
		}
	}
}

func TestSampleEdgeRoutingValidatesAllEvents(t *testing.T) {
	t.Helper()

	sample := SampleEdgeRouting()
	if len(sample.Messages) != 5 {
		t.Fatalf("expected 5 edge routing messages, got %d", len(sample.Messages))
	}

	targetParticipant := "agent.field.nullbot"
	if sample.Messages[1].To[0] != targetParticipant {
		t.Fatalf("expected task request target %q, got %#v", targetParticipant, sample.Messages[1].To)
	}

	for _, envelope := range sample.Messages {
		switch envelope.Type {
		case model.MessageTypeAgentSessionRegistered,
			model.MessageTypeAgentSessionHeartbeat,
			model.MessageTypeAgentDeliveryReceipt:
			if err := envelope.ValidateEdgeRouting(); err != nil {
				t.Fatalf("ValidateEdgeRouting(%s) error = %v", envelope.Type, err)
			}
		default:
			if err := envelope.Validate(); err != nil {
				t.Fatalf("Validate(%s) error = %v", envelope.Type, err)
			}
		}
	}
}

func TestSampleCaseClarificationPayloadUsesStructuredAuthorization(t *testing.T) {
	t.Helper()

	sample := SampleCase()

	var payload model.ClarificationRequestPayload
	if err := json.Unmarshal(sample.Messages[1].Payload, &payload); err != nil {
		t.Fatalf("Unmarshal(clarification payload) error = %v", err)
	}

	if err := payload.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestSampleClarificationLifecycleValidatesAllEvents(t *testing.T) {
	t.Helper()

	sample := SampleClarificationLifecycle()
	if len(sample.Messages) != 4 {
		t.Fatalf("expected 4 clarification lifecycle messages, got %d", len(sample.Messages))
	}

	for _, envelope := range sample.Messages {
		switch envelope.Type {
		case model.MessageTypeClarifyRequest:
			var payload model.ClarificationRequestPayload
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				t.Fatalf("Unmarshal(clarification request) error = %v", err)
			}
			if err := payload.Validate(); err != nil {
				t.Fatalf("ClarificationRequestPayload.Validate() error = %v", err)
			}
		case model.MessageTypeClarifyReceipt, model.MessageTypeClarifyOutcome:
			if err := envelope.ValidateClarificationLifecycle(); err != nil {
				t.Fatalf("ValidateClarificationLifecycle(%s) error = %v", envelope.Type, err)
			}
		default:
			t.Fatalf("unexpected message type %s", envelope.Type)
		}
	}
}

func TestSampleParticipantModelValidatesAllEvents(t *testing.T) {
	t.Helper()

	sample := SampleParticipantModel()
	if len(sample.Messages) != 3 {
		t.Fatalf("expected 3 participant model messages, got %d", len(sample.Messages))
	}

	if err := sample.Thread.Validate(); err != nil {
		t.Fatalf("Thread.Validate() error = %v", err)
	}

	if sample.Messages[0].From != "human.operator" || sample.Messages[0].To[0] != "human.reporter" {
		t.Fatalf("expected human-to-human traffic, got from=%q to=%#v", sample.Messages[0].From, sample.Messages[0].To)
	}

	if err := sample.Messages[0].ValidateTaskRequest(); err != nil {
		t.Fatalf("ValidateTaskRequest(human-human) error = %v", err)
	}

	if err := sample.Messages[1].ValidateTaskResultFinal(sample.Messages[0]); err != nil {
		t.Fatalf("ValidateTaskResultFinal(human-human) error = %v", err)
	}

	if err := sample.Messages[2].ValidateTaskRequest(); err != nil {
		t.Fatalf("ValidateTaskRequest(human-agent) error = %v", err)
	}
}
