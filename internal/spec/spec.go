package spec

import (
	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/policy"
)

// Document is the compact machine contract for the v0 protocol.
type Document struct {
	Name            string                       `json:"name"`
	Version         string                       `json:"version"`
	Description     string                       `json:"description"`
	MessageTypes    []model.MessageType          `json:"message_types"`
	ThreadStatuses  []model.ThreadStatus         `json:"thread_statuses"`
	TierLimits      map[model.Tier]policy.Limits `json:"tier_limits"`
	EditionBoundary policy.EditionBoundary       `json:"edition_boundary"`
	FeatureBoundary policy.FeatureBoundary       `json:"feature_boundary"`
	TrackingSystem  string                       `json:"tracking_system"`
	OptionalBridge  string                       `json:"optional_bridge,omitempty"`
	WorkOrderGate   map[string]bool              `json:"work_order_gate"`
}

// V0 returns the initial protocol contract for Hivebus.
func V0() Document {
	return Document{
		Name:        "hivebus",
		Version:     "0.1.0",
		Description: "Secure threaded coordination for agent-native issue intake and work-order creation.",
		MessageTypes: []model.MessageType{
			model.MessageTypeTaskRequest,
			model.MessageTypeTaskAccepted,
			model.MessageTypeTaskResultPart,
			model.MessageTypeTaskResultFinal,
			model.MessageTypeClarifyRequest,
			model.MessageTypeClarifyResponse,
			model.MessageTypeEvidenceCaptured,
			model.MessageTypeDiagnosisPropose,
			model.MessageTypeWorkOrderCreate,
			model.MessageTypeTaskCancel,
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
