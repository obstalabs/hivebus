package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

const (
	maxNRRunRefLength     = 512
	maxNRRunSummaryLength = 1200
)

var nrRunMessageTypes = []MessageType{
	MessageTypeNRRunStarted,
	MessageTypeNRRunContextProjected,
	MessageTypeNRRunApprovalPending,
	MessageTypeNRRunToolCall,
	MessageTypeNRRunPolicyDenied,
	MessageTypeNRRunCompleted,
	MessageTypeNRRunFailed,
	MessageTypeNRRunCancelled,
	MessageTypeNRRunAuditAnchor,
}

// NRRunPolicyDecision records the broker decision that produced an nr.run.* receipt. // WO-47
type NRRunPolicyDecision string

const (
	NRRunPolicyAllowed          NRRunPolicyDecision = "allowed"
	NRRunPolicyDenied           NRRunPolicyDecision = "denied"
	NRRunPolicyApprovalRequired NRRunPolicyDecision = "approval_required"
)

var validNRRunPolicyDecisions = []NRRunPolicyDecision{
	NRRunPolicyAllowed,
	NRRunPolicyDenied,
	NRRunPolicyApprovalRequired,
}

// NRRunPolicyResult is a references-only policy receipt for a governed run. // WO-47
type NRRunPolicyResult struct {
	Decision NRRunPolicyDecision `json:"decision"`           // WO-47: tool-policy decision, not copied policy body
	RuleRef  string              `json:"rule_ref,omitempty"` // WO-47: stable policy rule reference
	Reason   string              `json:"reason,omitempty"`   // WO-47: redacted operator-safe reason
}

// NRRunCostSummary stores bounded metering metadata without provider transcripts. // WO-47
type NRRunCostSummary struct {
	Currency     string  `json:"currency"`      // WO-47: ISO-like display currency
	InputTokens  int64   `json:"input_tokens"`  // WO-47: prompt-side metering total
	OutputTokens int64   `json:"output_tokens"` // WO-47: completion-side metering total
	Total        float64 `json:"total"`         // WO-47: run cost in currency
}

// NRRunArtifactRef points at an external artifact rather than copying it. // WO-47
type NRRunArtifactRef struct {
	Ref      string `json:"ref"`                // WO-47: stable artifact reference
	Kind     string `json:"kind"`               // WO-47: artifact class for readers
	SHA256   string `json:"sha256,omitempty"`   // WO-47: optional immutable content hash
	Redacted bool   `json:"redacted,omitempty"` // WO-47: true when artifact view is scrubbed
}

// NRRunPayload is the references-only payload shared by all nr.run.* envelopes. // WO-47
type NRRunPayload struct {
	WorkledgerWORef   string             `json:"workledger_wo_ref,omitempty"`       // WO-47: canonical WO reference
	AgentBundleRef    string             `json:"agent_bundle_ref,omitempty"`        // WO-47: Agent Bundle id reference
	AgentBundleHash   string             `json:"agent_bundle_hash,omitempty"`       // WO-47: Agent Bundle content hash
	ContextBundleRef  string             `json:"context_bundle_ref,omitempty"`      // WO-47: Context Bundle id reference
	ContextBundleHash string             `json:"context_bundle_hash,omitempty"`     // WO-47: Context Bundle content hash
	ToolPolicyRef     string             `json:"tool_policy_ref,omitempty"`         // WO-47: Tool Policy id reference
	ToolPolicyHash    string             `json:"tool_policy_hash,omitempty"`        // WO-47: Tool Policy content hash
	NeuroRouterRunID  string             `json:"neurorouter_run_id"`                // WO-47: canonical run id
	AuditAnchorID     string             `json:"audit_anchor_id,omitempty"`         // WO-47: canonical audit-anchor id
	SourceThreadID    string             `json:"source_thread_id"`                  // WO-47: source Hivebus thread id
	SourceEnvelopeID  string             `json:"source_envelope_id,omitempty"`      // WO-47: source envelope id when applicable
	Model             string             `json:"model,omitempty"`                   // WO-47: provider-facing model name
	Provider          string             `json:"provider,omitempty"`                // WO-47: provider name
	Cost              *NRRunCostSummary  `json:"cost,omitempty"`                    // WO-47: bounded metering summary
	Policy            *NRRunPolicyResult `json:"policy,omitempty"`                  // WO-47: policy decision summary
	RedactedOutput    string             `json:"redacted_output_summary,omitempty"` // WO-47: bounded scrubbed output summary
	ArtifactRefs      []NRRunArtifactRef `json:"artifact_refs,omitempty"`           // WO-47: refs to artifacts, never copied bodies
	ToolCallID        string             `json:"tool_call_id,omitempty"`            // WO-47: tool-call id for tool receipts
	ToolName          string             `json:"tool_name,omitempty"`               // WO-47: tool name, not payload
	ApprovalID        string             `json:"approval_id,omitempty"`             // WO-47: approval workflow reference
	FailureReason     string             `json:"failure_reason,omitempty"`          // WO-47: redacted failure reason
	OccurredAt        string             `json:"occurred_at"`                       // WO-47: RFC3339 run-event time
	Redacted          bool               `json:"redacted"`                          // WO-47: customer-safe view marker
}

// IsNRRunMessageType reports whether a message type belongs to the nr.run.* family. // WO-47
func IsNRRunMessageType(messageType MessageType) bool {
	return slices.Contains(nrRunMessageTypes, messageType)
}

// NRRunMessageTypes returns the governed NeuroRouter run-lifecycle event types. // WO-47
func NRRunMessageTypes() []MessageType {
	return slices.Clone(nrRunMessageTypes)
}

// ValidateNRRunLifecycle applies strict references-not-copies validation to nr.run.* envelopes. // WO-47
func (e Envelope) ValidateNRRunLifecycle() error {
	if err := e.validateBase(); err != nil {
		return err
	}

	return e.validateNRRunLifecyclePayload()
}

func (e Envelope) validateNRRunLifecyclePayload() error {
	if !IsNRRunMessageType(e.Type) {
		return fmt.Errorf("expected nr.run.* event, got %q", e.Type)
	}

	var payload NRRunPayload
	decoder := json.NewDecoder(bytes.NewReader(e.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("decode nr.run payload: %w", err)
	}
	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("nr.run payload must contain one json object")
	}

	if err := payload.Validate(e.Type, e.ThreadID); err != nil {
		return err
	}

	return nil
}

// Validate checks that an nr.run.* payload is bounded, redacted, and reference-only. // WO-47
func (p NRRunPayload) Validate(eventType MessageType, envelopeThreadID string) error {
	switch {
	case strings.TrimSpace(p.NeuroRouterRunID) == "":
		return errors.New("neurorouter_run_id is required")
	case strings.TrimSpace(p.SourceThreadID) == "":
		return errors.New("source_thread_id is required")
	case strings.TrimSpace(p.SourceThreadID) != strings.TrimSpace(envelopeThreadID):
		return errors.New("source_thread_id must match envelope thread_id")
	case strings.TrimSpace(p.OccurredAt) == "":
		return errors.New("occurred_at is required")
	case !p.Redacted:
		return errors.New("nr.run payload must be redacted")
	case len(p.RedactedOutput) > maxNRRunSummaryLength:
		return errors.New("redacted_output_summary is too long")
	}

	if _, err := time.Parse(time.RFC3339, p.OccurredAt); err != nil {
		return fmt.Errorf("occurred_at must be RFC3339: %w", err)
	}

	if err := p.validateRefs(); err != nil {
		return err
	}
	if p.Cost != nil {
		if err := p.Cost.Validate(); err != nil {
			return fmt.Errorf("cost: %w", err)
		}
	}
	if p.Policy != nil {
		if err := p.Policy.Validate(); err != nil {
			return fmt.Errorf("policy: %w", err)
		}
	}
	for i, artifact := range p.ArtifactRefs {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("artifact_refs[%d]: %w", i, err)
		}
	}

	switch eventType {
	case MessageTypeNRRunStarted:
		if strings.TrimSpace(p.WorkledgerWORef) == "" {
			return errors.New("nr.run.started requires workledger_wo_ref")
		}
		if !p.hasAgentBundleRef() {
			return errors.New("nr.run.started requires agent bundle ref or hash")
		}
		if !p.hasContextBundleRef() {
			return errors.New("nr.run.started requires context bundle ref or hash")
		}
		if !p.hasToolPolicyRef() {
			return errors.New("nr.run.started requires tool policy ref or hash")
		}
	case MessageTypeNRRunContextProjected:
		if !p.hasContextBundleRef() {
			return errors.New("nr.run.context_projected requires context bundle ref or hash")
		}
	case MessageTypeNRRunApprovalPending:
		if strings.TrimSpace(p.ApprovalID) == "" {
			return errors.New("nr.run.approval_pending requires approval_id")
		}
		if p.Policy == nil || p.Policy.Decision != NRRunPolicyApprovalRequired {
			return errors.New("nr.run.approval_pending requires approval_required policy decision")
		}
	case MessageTypeNRRunToolCall:
		if strings.TrimSpace(p.ToolCallID) == "" {
			return errors.New("nr.run.tool_call requires tool_call_id")
		}
		if strings.TrimSpace(p.ToolName) == "" {
			return errors.New("nr.run.tool_call requires tool_name")
		}
		if p.Policy == nil {
			return errors.New("nr.run.tool_call requires policy")
		}
	case MessageTypeNRRunPolicyDenied:
		if p.Policy == nil || p.Policy.Decision != NRRunPolicyDenied {
			return errors.New("nr.run.policy_denied requires denied policy decision")
		}
		if strings.TrimSpace(p.Policy.Reason) == "" {
			return errors.New("nr.run.policy_denied requires policy.reason")
		}
	case MessageTypeNRRunCompleted:
		if strings.TrimSpace(p.RedactedOutput) == "" && len(p.ArtifactRefs) == 0 {
			return errors.New("nr.run.completed requires redacted output summary or artifact refs")
		}
		if strings.TrimSpace(p.FailureReason) != "" {
			return errors.New("nr.run.completed must not set failure_reason")
		}
	case MessageTypeNRRunFailed:
		if strings.TrimSpace(p.FailureReason) == "" {
			return errors.New("nr.run.failed requires failure_reason")
		}
	case MessageTypeNRRunCancelled:
		if strings.TrimSpace(p.FailureReason) == "" {
			return errors.New("nr.run.cancelled requires failure_reason")
		}
	case MessageTypeNRRunAuditAnchor:
		if strings.TrimSpace(p.AuditAnchorID) == "" {
			return errors.New("nr.run.audit_anchor requires audit_anchor_id")
		}
	default:
		return fmt.Errorf("unsupported nr.run event %q", eventType)
	}

	return nil
}

// Validate checks a bounded cost summary for an nr.run.* receipt. // WO-47
func (c NRRunCostSummary) Validate() error {
	switch {
	case strings.TrimSpace(c.Currency) == "":
		return errors.New("currency is required")
	case c.InputTokens < 0:
		return errors.New("input_tokens must be zero or positive")
	case c.OutputTokens < 0:
		return errors.New("output_tokens must be zero or positive")
	case c.Total < 0:
		return errors.New("total must be zero or positive")
	}

	return nil
}

// Validate checks a policy decision summary for an nr.run.* receipt. // WO-47
func (p NRRunPolicyResult) Validate() error {
	if !slices.Contains(validNRRunPolicyDecisions, p.Decision) {
		return fmt.Errorf("unsupported policy decision %q", p.Decision)
	}
	if err := validateOptionalNRRunRef("rule_ref", p.RuleRef); err != nil {
		return err
	}
	if len(p.Reason) > maxNRRunSummaryLength {
		return errors.New("policy.reason is too long")
	}

	return nil
}

// Validate checks that an artifact is represented as a stable ref. // WO-47
func (r NRRunArtifactRef) Validate() error {
	switch {
	case strings.TrimSpace(r.Ref) == "":
		return errors.New("ref is required")
	case strings.TrimSpace(r.Kind) == "":
		return errors.New("kind is required")
	case strings.TrimSpace(r.SHA256) != "" && !IsSHA256Hex(strings.TrimSpace(r.SHA256)):
		return errors.New("sha256 must be a 64-character lowercase hex digest")
	}

	if err := validateOptionalNRRunRef("ref", r.Ref); err != nil {
		return err
	}
	if err := validateOptionalNRRunRef("kind", r.Kind); err != nil {
		return err
	}

	return validateOptionalNRRunRef("sha256", r.SHA256)
}

func (p NRRunPayload) validateRefs() error {
	refs := map[string]string{
		"workledger_wo_ref":   p.WorkledgerWORef,
		"agent_bundle_ref":    p.AgentBundleRef,
		"agent_bundle_hash":   p.AgentBundleHash,
		"context_bundle_ref":  p.ContextBundleRef,
		"context_bundle_hash": p.ContextBundleHash,
		"tool_policy_ref":     p.ToolPolicyRef,
		"tool_policy_hash":    p.ToolPolicyHash,
		"audit_anchor_id":     p.AuditAnchorID,
		"source_envelope_id":  p.SourceEnvelopeID,
		"model":               p.Model,
		"provider":            p.Provider,
		"tool_call_id":        p.ToolCallID,
		"tool_name":           p.ToolName,
		"approval_id":         p.ApprovalID,
	}

	for name, value := range refs {
		if err := validateOptionalNRRunRef(name, value); err != nil {
			return err
		}
	}

	if len(p.FailureReason) > maxNRRunSummaryLength {
		return errors.New("failure_reason is too long")
	}

	return nil
}

func validateOptionalNRRunRef(name string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) > maxNRRunRefLength {
		return fmt.Errorf("%s is too long", name)
	}
	if strings.ContainsAny(value, "\r\n\t") {
		return fmt.Errorf("%s must be a single-line reference", name)
	}

	return nil
}

func (p NRRunPayload) hasAgentBundleRef() bool {
	return strings.TrimSpace(p.AgentBundleRef) != "" || strings.TrimSpace(p.AgentBundleHash) != ""
}

func (p NRRunPayload) hasContextBundleRef() bool {
	return strings.TrimSpace(p.ContextBundleRef) != "" || strings.TrimSpace(p.ContextBundleHash) != ""
}

func (p NRRunPayload) hasToolPolicyRef() bool {
	return strings.TrimSpace(p.ToolPolicyRef) != "" || strings.TrimSpace(p.ToolPolicyHash) != ""
}
