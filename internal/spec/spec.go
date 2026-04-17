package spec

import (
	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/policy"
)

// Document is the compact machine contract for the v0 protocol.
type Document struct {
	Name                   string                       `json:"name"`
	Version                string                       `json:"version"`
	Description            string                       `json:"description"`
	MessageTypes           []model.MessageType          `json:"message_types"`
	CapabilityLifecycle    CapabilityLifecycleContract  `json:"capability_lifecycle"`
	ThreadStatuses         []model.ThreadStatus         `json:"thread_statuses"`
	ArtifactManifestFields []string                     `json:"artifact_manifest_fields"`
	TierLimits             map[model.Tier]policy.Limits `json:"tier_limits"`
	EditionBoundary        policy.EditionBoundary       `json:"edition_boundary"`
	FeatureBoundary        policy.FeatureBoundary       `json:"feature_boundary"`
	TrackingSystem         string                       `json:"tracking_system"`
	OptionalBridge         string                       `json:"optional_bridge,omitempty"`
	WorkOrderGate          map[string]bool              `json:"work_order_gate"`
}

type CapabilityLifecycleContract struct {
	MessageTypes   []model.MessageType `json:"message_types"`
	RequiredFields []string            `json:"required_fields"`
	DeliveryModes  []string            `json:"delivery_modes"`
	RefusalReasons []string            `json:"refusal_reasons"`
	TrustRoots     []string            `json:"trust_roots"`
	Consumers      map[string][]string `json:"consumers"`
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
