package spec

import (
	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/policy"
)

// Document is the compact machine contract for the v0 protocol.
type Document struct {
	Name                   string                         `json:"name"`
	Version                string                         `json:"version"`
	Description            string                         `json:"description"`
	MessageTypes           []model.MessageType            `json:"message_types"`
	Participants           ParticipantContract            `json:"participants"`
	Authorization          AuthorizationContract          `json:"authorization"`
	ClarificationLifecycle ClarificationLifecycleContract `json:"clarification_lifecycle"`
	EdgeRouting            EdgeRoutingContract            `json:"edge_routing"`
	CapabilityLifecycle    CapabilityLifecycleContract    `json:"capability_lifecycle"`
	ThreadStatuses         []model.ThreadStatus           `json:"thread_statuses"`
	ArtifactManifestFields []string                       `json:"artifact_manifest_fields"`
	TierLimits             map[model.Tier]policy.Limits   `json:"tier_limits"`
	EditionBoundary        policy.EditionBoundary         `json:"edition_boundary"`
	FeatureBoundary        policy.FeatureBoundary         `json:"feature_boundary"`
	TrackingSystem         string                         `json:"tracking_system"`
	OptionalBridge         string                         `json:"optional_bridge,omitempty"`
	WorkOrderGate          map[string]bool                `json:"work_order_gate"`
}

type CapabilityLifecycleContract struct {
	MessageTypes   []model.MessageType `json:"message_types"`
	RequiredFields []string            `json:"required_fields"`
	DeliveryModes  []string            `json:"delivery_modes"`
	RefusalReasons []string            `json:"refusal_reasons"`
	TrustRoots     []string            `json:"trust_roots"`
	Consumers      map[string][]string `json:"consumers"`
}

type ParticipantContract struct {
	Types              []string            `json:"types"`
	SharedFields       []string            `json:"shared_fields"`
	TypeSpecificFields map[string][]string `json:"type_specific_fields"`
	MembershipRules    map[string][]string `json:"membership_rules"`
	VisibilityRules    map[string][]string `json:"visibility_rules"`
	AuthorizationRules map[string][]string `json:"authorization_rules"`
}

type EdgeRoutingContract struct {
	MessageTypes  []model.MessageType `json:"message_types"`
	SessionFields []string            `json:"session_fields"`
	ReceiptFields []string            `json:"receipt_fields"`
	DeliveryModes []string            `json:"delivery_modes"`
	SessionStates []string            `json:"session_states"`
	ReceiptStates []string            `json:"receipt_states"`
	RoutingRule   string              `json:"routing_rule"`
	Consumers     map[string][]string `json:"consumers"`
}

type AuthorizationContract struct {
	RequiredFields   []string            `json:"required_fields"`
	RequestClasses   []string            `json:"request_classes"`
	ApprovalStates   []string            `json:"approval_states"`
	RefusalReasons   []string            `json:"refusal_reasons"`
	MembershipStates []string            `json:"membership_states"`
	DecisionPoints   []string            `json:"decision_points"`
	Consumers        map[string][]string `json:"consumers"`
}

type ClarificationLifecycleContract struct {
	ReceiptStates    []string            `json:"receipt_states"`
	FailureStates    []string            `json:"failure_states"`
	TerminalOutcomes []string            `json:"terminal_outcomes"`
	BudgetFields     []string            `json:"budget_fields"`
	Consumers        map[string][]string `json:"consumers"`
}

// V0 returns the initial protocol contract for Hivebus.
func V0() Document {
	return Document{
		Name:        "hivebus",
		Version:     "0.1.1",
		Description: "Secure threaded coordination for agent-native issue intake and work-order creation.",
		MessageTypes: []model.MessageType{
			model.MessageTypeTaskRequest,
			model.MessageTypeTaskAccepted,
			model.MessageTypeTaskResultPart,
			model.MessageTypeTaskResultFinal,
			model.MessageTypeClarifyRequest,
			model.MessageTypeClarifyResponse,
			model.MessageTypeAgentSessionRegistered,
			model.MessageTypeAgentSessionHeartbeat,
			model.MessageTypeAgentDeliveryReceipt,
			model.MessageTypeInstallRequested,
			model.MessageTypeInstallVerified,
			model.MessageTypeDoctorPassed,
			model.MessageTypeDoctorFailed,
			model.MessageTypeCapabilityActive,
			model.MessageTypeTaskCompleted,
			model.MessageTypeTeardownRequested,
			model.MessageTypeTeardownCompleted,
			model.MessageTypeTeardownFailed,
			model.MessageTypeEvidenceCaptured,
			model.MessageTypeDiagnosisPropose,
			model.MessageTypeWorkOrderCreate,
			model.MessageTypeTaskCancel,
		},
		Participants: ParticipantContract{
			Types: []string{
				string(model.ParticipantTypeHuman),
				string(model.ParticipantTypeAgent),
				string(model.ParticipantTypeService),
			},
			SharedFields: []string{
				"id",
				"participant_type",
				"display_name",
				"visibility",
				"capabilities",
			},
			TypeSpecificFields: map[string][]string{
				"human": {
					"human.human_id",
					"human.role",
					"human.delivery_preference",
				},
				"agent": {
					"agent.agent_id",
					"agent.installation_id",
				},
				"service": {
					"service.service_name",
				},
			},
			MembershipRules: map[string][]string{
				"human": {
					"humans are explicit thread participants; no side channel is required for human-to-human traffic",
					"human requests still use the same envelope and thread membership rules as agents and services",
				},
				"agent": {
					"agents participate in the thread directly and pair with WO-9 session routing by participant_id",
					"agent membership does not replace authorization checks from WO-10",
				},
				"service": {
					"services may be thread-visible or internal, but they still keep first-class participant identity",
					"service actors remain addressable without becoming implicit transport adapters",
				},
			},
			VisibilityRules: map[string][]string{
				"human": {
					"human participants are always thread-visible",
				},
				"agent": {
					"agents default to thread visibility so replies stay in the same transcript",
				},
				"service": {
					"services may be thread-visible or internal depending on whether they speak in the thread or only coordinate it",
				},
			},
			AuthorizationRules: map[string][]string{
				"human": {
					"human senders remain first-class authorization subjects instead of a special-case UI path",
				},
				"agent": {
					"agent requests compose with structured sender_participant_id and participant_membership from the authorization contract",
				},
				"service": {
					"service participants keep the same sender identity and approval surface as every other actor type",
				},
			},
		},
		Authorization: AuthorizationContract{
			RequiredFields: []string{
				"sender_participant_id",
				"participant_membership",
				"requested_scope",
				"request_class",
				"approval_state",
				"expires_at",
				"refusal_reason",
			},
			RequestClasses: []string{
				string(model.AuthorizationRequestClarification),
				string(model.AuthorizationRequestCapability),
				string(model.AuthorizationRequestEvidence),
			},
			ApprovalStates: []string{
				string(model.AuthorizationNotRequired),
				string(model.AuthorizationPending),
				string(model.AuthorizationApproved),
				string(model.AuthorizationDenied),
			},
			RefusalReasons: []string{
				string(model.AuthorizationUnauthorizedSender),
				string(model.AuthorizationScopeMismatch),
				string(model.AuthorizationExpiredRequest),
				string(model.AuthorizationForbiddenCapability),
				string(model.AuthorizationMissingApproval),
			},
			MembershipStates: []string{
				string(model.ParticipantMembershipThreadParticipant),
				string(model.ParticipantMembershipServiceParticipant),
				string(model.ParticipantMembershipNonParticipant),
			},
			DecisionPoints: []string{
				"clarification.request",
				"evidence request",
				"capability.install.requested",
			},
			Consumers: map[string][]string{
				"field_agent": {
					"authorize clarification and capability requests from structured sender identity, membership, scope, class, expiry, and approval state",
					"reject requests structurally when approval_state or refusal_reason says deny",
				},
				"policy": {
					"separate thread-bound authorization from transport reachability",
					"treat forbidden capability and missing approval as explicit refusal outcomes",
				},
			},
		},
		ClarificationLifecycle: ClarificationLifecycleContract{
			ReceiptStates: []string{
				string(model.ClarificationReceiptQueued),
				string(model.ClarificationReceiptDelivered),
				string(model.ClarificationReceiptUnanswered),
				string(model.ClarificationReceiptRefused),
				string(model.ClarificationReceiptDuplicate),
				string(model.ClarificationReceiptStale),
				string(model.ClarificationReceiptSessionSwap),
			},
			FailureStates: []string{
				string(model.ClarificationFailureDuplicateRequest),
				string(model.ClarificationFailureStaleRequest),
				string(model.ClarificationFailureStaleResponse),
				string(model.ClarificationFailureConflicting),
				string(model.ClarificationFailureExpired),
				string(model.ClarificationFailureThreadFinalized),
				string(model.ClarificationFailureSessionReplaced),
				string(model.ClarificationFailureMaxRounds),
			},
			TerminalOutcomes: []string{
				string(model.ClarificationOutcomeReadyForWO),
				string(model.ClarificationOutcomeNeedsHuman),
				string(model.ClarificationOutcomeAbandoned),
			},
			BudgetFields: []string{
				"max_rounds",
				"max_evidence_bytes",
				"rounds_used",
				"evidence_bytes",
			},
			Consumers: map[string][]string{
				"sentinel": {
					"consume duplicate, stale, expired, session-replaced, and max-round failures structurally",
					"read terminal clarification outcomes without parsing prose",
				},
				"workledger": {
					"map ready_for_wo, needs_human, and abandoned outcomes into notes or lifecycle state",
					"preserve clarification failure_state for audit instead of inferring from missing replies",
				},
				"runtime": {
					"latest-session-wins is represented by clarification.session_replaced receipt state",
					"queued, delivered, unanswered, and refused are explicit delivery outcomes",
				},
			},
		},
		EdgeRouting: EdgeRoutingContract{
			MessageTypes: []model.MessageType{
				model.MessageTypeAgentSessionRegistered,
				model.MessageTypeAgentSessionHeartbeat,
				model.MessageTypeAgentDeliveryReceipt,
			},
			SessionFields: []string{
				"agent_id",
				"installation_id",
				"session_id",
				"participant_id",
				"capabilities",
				"delivery_mode",
				"session_status",
				"lease_expires_at",
				"host_alias",
				"replaces_session_id",
			},
			ReceiptFields: []string{
				"target_participant_id",
				"target_agent_id",
				"target_session_id",
				"state",
				"expires_at",
				"queue_position",
				"reason",
			},
			DeliveryModes: []string{
				string(model.AgentDeliveryOutboundOnly),
				string(model.AgentDeliveryQueued),
			},
			SessionStates: []string{
				string(model.AgentSessionOnline),
				string(model.AgentSessionOffline),
				string(model.AgentSessionReplaced),
			},
			ReceiptStates: []string{
				string(model.DeliveryReceiptQueued),
				string(model.DeliveryReceiptDelivered),
				string(model.DeliveryReceiptExpired),
				string(model.DeliveryReceiptRefused),
			},
			RoutingRule: "Replies target participant_id inside the thread; session registration resolves that participant to the current edge session without exposing host IP or callback URLs.",
			Consumers: map[string][]string{
				"hivebus": {
					"store session leases by participant_id and installation_id",
					"treat queued and delivered receipts as explicit delivery evidence",
				},
				"operator": {
					"inspect host_alias and lease state for diagnostics only",
					"never target raw network location from thread messages",
				},
			},
		},
		CapabilityLifecycle: CapabilityLifecycleContract{
			MessageTypes: []model.MessageType{
				model.MessageTypeInstallRequested,
				model.MessageTypeInstallVerified,
				model.MessageTypeDoctorPassed,
				model.MessageTypeDoctorFailed,
				model.MessageTypeCapabilityActive,
				model.MessageTypeTaskCompleted,
				model.MessageTypeTeardownRequested,
				model.MessageTypeTeardownCompleted,
				model.MessageTypeTeardownFailed,
			},
			RequiredFields: []string{
				"host",
				"capability_id",
				"capability_class",
				"version",
				"digest",
				"artifact_ref",
				"attestation_ref",
				"signer",
				"trust_root",
				"requested_by",
				"approved_by",
				"origin_thread_id|origin_work_order_id",
				"expires_at",
				"delivery_mode",
				"evidence_ids",
				"attestation_state",
				"task_outcome",
				"teardown_state",
				"refusal_reason",
				"failure_reason",
			},
			DeliveryModes: []string{
				string(model.CapabilityDeliveryReference),
				string(model.CapabilityDeliveryInlineException),
			},
			RefusalReasons: []string{
				string(model.CapabilityRefusalUnknownSigner),
				string(model.CapabilityRefusalExpiredArtifact),
				string(model.CapabilityRefusalClassMismatch),
				string(model.CapabilityRefusalPolicyDenied),
			},
			TrustRoots: []string{
				"obstalabs-release-root",
			},
			Consumers: map[string][]string{
				"sentinel": {
					"consume every lifecycle event and explicit failure state without parsing prose",
					"treat doctor_failed and teardown_failed as terminal failure signals",
				},
				"workledger": {
					"map install_verified, doctor_passed, doctor_failed, capability_active, task_completed, teardown_completed, and teardown_failed into work-order notes or lifecycle state",
					"never infer attestation or teardown success from missing events",
				},
				"policy": {
					"reject unknown signer, expired artifact, class mismatch, or policy-denied install requests structurally",
					"treat inline capability transport as exceptional and policy-gated",
				},
			},
		},
		ThreadStatuses: []model.ThreadStatus{
			model.ThreadStatusReported,
			model.ThreadStatusCollecting,
			model.ThreadStatusInvestigating,
			model.ThreadStatusWaiting,
			model.ThreadStatusReadyForWork,
			model.ThreadStatusDone,
			model.ThreadStatusFailed,
			model.ThreadStatusCancelled,
		},
		ArtifactManifestFields: []string{
			"sha256",
			"size_bytes",
			"content_type",
		},
		TierLimits: map[model.Tier]policy.Limits{
			model.TierFree:       policy.LimitsFor(model.TierFree),
			model.TierPro:        policy.LimitsFor(model.TierPro),
			model.TierTeams:      policy.LimitsFor(model.TierTeams),
			model.TierEnterprise: policy.LimitsFor(model.TierEnterprise),
		},
		EditionBoundary: policy.Boundary(),
		FeatureBoundary: policy.Features(),
		TrackingSystem:  "workledger",
		OptionalBridge:  "hiveram.com",
		WorkOrderGate: map[string]bool{
			"verified_diagnosis_required": true,
			"missing_info_must_be_empty":  true,
			"workledger_project_required": true,
		},
	}
}
