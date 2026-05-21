package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// MessageType identifies the intent of an envelope.
type MessageType string

const (
	MessageTypeTaskRequest            MessageType = "task.request"
	MessageTypeTaskAccepted           MessageType = "task.accepted"
	MessageTypeTaskResultPart         MessageType = "task.result.partial"
	MessageTypeTaskResultFinal        MessageType = "task.result.final"
	MessageTypeClarifyRequest         MessageType = "clarification.request"
	MessageTypeClarifyResponse        MessageType = "clarification.response"
	MessageTypeClarifyReceipt         MessageType = "clarification.receipt"
	MessageTypeClarifyOutcome         MessageType = "clarification.outcome"
	MessageTypeAgentSessionRegistered MessageType = "agent.session.registered"
	MessageTypeAgentSessionHeartbeat  MessageType = "agent.session.heartbeat"
	MessageTypeAgentDeliveryReceipt   MessageType = "agent.delivery.receipt"
	MessageTypeInstallRequested       MessageType = "capability.install.requested"
	MessageTypeInstallVerified        MessageType = "capability.install.verified"
	MessageTypeDoctorPassed           MessageType = "capability.doctor.passed"
	MessageTypeDoctorFailed           MessageType = "capability.doctor.failed"
	MessageTypeCapabilityActive       MessageType = "capability.active"
	MessageTypeTaskCompleted          MessageType = "capability.task.completed"
	MessageTypeTeardownRequested      MessageType = "capability.teardown.requested"
	MessageTypeTeardownCompleted      MessageType = "capability.teardown.completed"
	MessageTypeTeardownFailed         MessageType = "capability.teardown.failed"
	MessageTypeEvidenceCaptured       MessageType = "evidence.captured"
	MessageTypeDiagnosisPropose       MessageType = "diagnosis.proposed"
	MessageTypeWorkOrderCreate        MessageType = "work_order.create"
	MessageTypeTaskCancel             MessageType = "task.cancel"
	MessageTypeNRRunStarted           MessageType = "nr.run.started"
	MessageTypeNRRunContextProjected  MessageType = "nr.run.context_projected"
	MessageTypeNRRunApprovalPending   MessageType = "nr.run.approval_pending"
	MessageTypeNRRunToolCall          MessageType = "nr.run.tool_call"
	MessageTypeNRRunPolicyDenied      MessageType = "nr.run.policy_denied"
	MessageTypeNRRunCompleted         MessageType = "nr.run.completed"
	MessageTypeNRRunFailed            MessageType = "nr.run.failed"
	MessageTypeNRRunCancelled         MessageType = "nr.run.cancelled"
	MessageTypeNRRunAuditAnchor       MessageType = "nr.run.audit_anchor"
)

var validMessageTypes = []MessageType{
	MessageTypeTaskRequest,
	MessageTypeTaskAccepted,
	MessageTypeTaskResultPart,
	MessageTypeTaskResultFinal,
	MessageTypeClarifyRequest,
	MessageTypeClarifyResponse,
	MessageTypeClarifyReceipt,
	MessageTypeClarifyOutcome,
	MessageTypeAgentSessionRegistered,
	MessageTypeAgentSessionHeartbeat,
	MessageTypeAgentDeliveryReceipt,
	MessageTypeInstallRequested,
	MessageTypeInstallVerified,
	MessageTypeDoctorPassed,
	MessageTypeDoctorFailed,
	MessageTypeCapabilityActive,
	MessageTypeTaskCompleted,
	MessageTypeTeardownRequested,
	MessageTypeTeardownCompleted,
	MessageTypeTeardownFailed,
	MessageTypeEvidenceCaptured,
	MessageTypeDiagnosisPropose,
	MessageTypeWorkOrderCreate,
	MessageTypeTaskCancel,
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

// Trace captures provenance for audit and replay.
type Trace struct {
	CorrelationID   string          `json:"correlation_id"`
	SpanID          string          `json:"span_id,omitempty"`
	Model           string          `json:"model,omitempty"`
	Verified        bool            `json:"verified"`
	PromotionStatus PromotionStatus `json:"promotion_status,omitempty"` // WO-54: recovery trusts only promotion-passed envelopes
}

// PromotionStatus labels whether a promotion-related envelope is still pending,
// failed before promotion completed, or passed the full gate.
type PromotionStatus string

const (
	PromotionStatusPending PromotionStatus = "pending"
	PromotionStatusPassed  PromotionStatus = "passed"
	PromotionStatusFailed  PromotionStatus = "failed"
)

// Security describes the transport-level security posture of an envelope.
type Security struct {
	Scheme    string `json:"scheme"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature,omitempty"`
	Signed    bool   `json:"signed"`
}

// Envelope is the machine-native message unit that moves through the bus.
type Envelope struct {
	MessageID      string          `json:"message_id"`
	ThreadID       string          `json:"thread_id"`
	From           string          `json:"from"`
	To             []string        `json:"to,omitempty"`
	Type           MessageType     `json:"type"`
	Capability     string          `json:"capability,omitempty"`
	Payload        json.RawMessage `json:"payload"`
	ArtifactIDs    []string        `json:"artifact_ids,omitempty"`
	ReplyTo        string          `json:"reply_to,omitempty"`
	Deadline       *time.Time      `json:"deadline,omitempty"`
	SentAt         time.Time       `json:"sent_at"`
	IdempotencyKey string          `json:"idempotency_key"`
	Trace          Trace           `json:"trace"`
	Security       Security        `json:"security"`
}

// Artifact stores evidence metadata without inlining unbounded blobs into the thread.
type Artifact struct {
	ArtifactID  string `json:"artifact_id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	URI         string `json:"uri"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentType string `json:"content_type"`
	Redacted    bool   `json:"redacted"`
}

// Validate applies structural checks that must hold before any routing.
func (e Envelope) Validate() error {
	if err := e.validateBase(); err != nil {
		return err
	}

	if IsNRRunMessageType(e.Type) {
		return e.validateNRRunLifecyclePayload()
	}

	return nil
}

func (e Envelope) validateBase() error {
	switch {
	case strings.TrimSpace(e.MessageID) == "":
		return errors.New("message_id is required")
	case strings.TrimSpace(e.ThreadID) == "":
		return errors.New("thread_id is required")
	case strings.TrimSpace(e.From) == "":
		return errors.New("from is required")
	case !slices.Contains(validMessageTypes, e.Type):
		return fmt.Errorf("unsupported type %q", e.Type)
	case len(e.To) == 0 && strings.TrimSpace(e.Capability) == "":
		return errors.New("either to or capability is required")
	case e.SentAt.IsZero():
		return errors.New("sent_at is required")
	case strings.TrimSpace(e.IdempotencyKey) == "":
		return errors.New("idempotency_key is required")
	case strings.TrimSpace(e.Trace.CorrelationID) == "":
		return errors.New("trace.correlation_id is required")
	case e.Trace.PromotionStatus != "" &&
		e.Trace.PromotionStatus != PromotionStatusPending &&
		e.Trace.PromotionStatus != PromotionStatusPassed &&
		e.Trace.PromotionStatus != PromotionStatusFailed:
		return fmt.Errorf("unsupported trace.promotion_status %q", e.Trace.PromotionStatus)
	case strings.TrimSpace(e.Security.Scheme) == "":
		return errors.New("security.scheme is required")
	case strings.TrimSpace(e.Security.Nonce) == "":
		return errors.New("security.nonce is required")
	}

	if len(e.Payload) > 0 && !json.Valid(e.Payload) {
		return errors.New("payload must be valid json")
	}

	if e.Deadline != nil && e.Deadline.Before(e.SentAt) {
		return errors.New("deadline must be after sent_at")
	}

	seen := make(map[string]struct{}, len(e.To))
	for _, recipient := range e.To {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			return errors.New("to contains an empty recipient")
		}

		if _, exists := seen[recipient]; exists {
			return fmt.Errorf("duplicate recipient %q", recipient)
		}

		seen[recipient] = struct{}{}
	}

	seenArtifactIDs := make(map[string]struct{}, len(e.ArtifactIDs))
	for _, artifactID := range e.ArtifactIDs {
		artifactID = strings.TrimSpace(artifactID)
		if artifactID == "" {
			return errors.New("artifact_ids contains an empty artifact id")
		}
		if _, exists := seenArtifactIDs[artifactID]; exists {
			return fmt.Errorf("duplicate artifact id %q", artifactID)
		}
		seenArtifactIDs[artifactID] = struct{}{}
	}

	return nil
}

// Validate applies structural checks to artifact metadata.
func (a Artifact) Validate() error {
	switch {
	case strings.TrimSpace(a.ArtifactID) == "":
		return errors.New("artifact_id is required")
	case strings.TrimSpace(a.Name) == "":
		return errors.New("name is required")
	case strings.TrimSpace(a.Kind) == "":
		return errors.New("kind is required")
	case strings.TrimSpace(a.URI) == "":
		return errors.New("uri is required")
	case strings.TrimSpace(a.SHA256) == "":
		return errors.New("sha256 is required")
	case a.SizeBytes < 0:
		return errors.New("size_bytes must be zero or positive")
	case strings.TrimSpace(a.ContentType) == "":
		return errors.New("content_type is required")
	case !strings.Contains(a.ContentType, "/"):
		return errors.New("content_type must be a media type")
	case !IsSHA256Hex(a.SHA256):
		return errors.New("sha256 must be a 64-character lowercase hex digest")
	}

	return nil
}

func DefaultContentType(body []byte) string {
	if len(body) == 0 {
		return "application/octet-stream"
	}

	return http.DetectContentType(body)
}

func IsSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}

	return true
}

// ValidateTaskRequest applies runtime checks for a claimable task envelope.
func (e Envelope) ValidateTaskRequest() error {
	if err := e.Validate(); err != nil {
		return err
	}
	if e.Type != MessageTypeTaskRequest {
		return fmt.Errorf("expected %q, got %q", MessageTypeTaskRequest, e.Type)
	}

	return nil
}

// ValidateTaskAccepted applies runtime checks for a task.accepted envelope.
func (e Envelope) ValidateTaskAccepted(request Envelope) error {
	if err := request.ValidateTaskRequest(); err != nil {
		return fmt.Errorf("invalid request envelope: %w", err)
	}
	if err := e.Validate(); err != nil {
		return err
	}
	if e.Type != MessageTypeTaskAccepted {
		return fmt.Errorf("expected %q, got %q", MessageTypeTaskAccepted, e.Type)
	}
	if e.ThreadID != request.ThreadID {
		return errors.New("task.accepted thread_id must match request thread_id")
	}
	if strings.TrimSpace(e.ReplyTo) != request.MessageID {
		return errors.New("task.accepted reply_to must match request message_id")
	}

	return nil
}

// ValidateTaskResultFinal applies runtime checks for a task.result.final envelope.
func (e Envelope) ValidateTaskResultFinal(request Envelope) error {
	if err := request.ValidateTaskRequest(); err != nil {
		return fmt.Errorf("invalid request envelope: %w", err)
	}
	if err := e.Validate(); err != nil {
		return err
	}
	if e.Type != MessageTypeTaskResultFinal {
		return fmt.Errorf("expected %q, got %q", MessageTypeTaskResultFinal, e.Type)
	}
	if e.ThreadID != request.ThreadID {
		return errors.New("task.result.final thread_id must match request thread_id")
	}
	if strings.TrimSpace(e.ReplyTo) != request.MessageID {
		return errors.New("task.result.final reply_to must match request message_id")
	}

	return nil
}

// ValidateTaskResultPart applies runtime checks for a task.result.partial envelope.
func (e Envelope) ValidateTaskResultPart(request Envelope) error {
	if err := request.ValidateTaskRequest(); err != nil {
		return fmt.Errorf("invalid request envelope: %w", err)
	}
	if err := e.Validate(); err != nil {
		return err
	}
	if e.Type != MessageTypeTaskResultPart {
		return fmt.Errorf("expected %q, got %q", MessageTypeTaskResultPart, e.Type)
	}
	if e.ThreadID != request.ThreadID {
		return errors.New("task.result.partial thread_id must match request thread_id")
	}
	if strings.TrimSpace(e.ReplyTo) != request.MessageID {
		return errors.New("task.result.partial reply_to must match request message_id")
	}

	return nil
}

// ValidateClarificationRequest applies runtime checks for a clarification.request envelope.
func (e Envelope) ValidateClarificationRequest(request Envelope) error {
	if err := request.ValidateTaskRequest(); err != nil {
		return fmt.Errorf("invalid request envelope: %w", err)
	}
	if err := e.Validate(); err != nil {
		return err
	}
	if e.Type != MessageTypeClarifyRequest {
		return fmt.Errorf("expected %q, got %q", MessageTypeClarifyRequest, e.Type)
	}
	if e.ThreadID != request.ThreadID {
		return errors.New("clarification.request thread_id must match request thread_id")
	}
	if strings.TrimSpace(e.ReplyTo) != request.MessageID {
		return errors.New("clarification.request reply_to must match request message_id")
	}
	if e.Deadline == nil {
		return errors.New("clarification.request deadline is required")
	}

	return nil
}

// ValidateClarificationResponse applies runtime checks for a clarification.response envelope.
func (e Envelope) ValidateClarificationResponse(
	taskRequest Envelope,
	clarificationRequest Envelope,
) error {
	if err := clarificationRequest.ValidateClarificationRequest(taskRequest); err != nil {
		return fmt.Errorf("invalid clarification request envelope: %w", err)
	}
	if err := e.Validate(); err != nil {
		return err
	}
	if e.Type != MessageTypeClarifyResponse {
		return fmt.Errorf("expected %q, got %q", MessageTypeClarifyResponse, e.Type)
	}
	if e.ThreadID != clarificationRequest.ThreadID {
		return errors.New("clarification.response thread_id must match clarification.request thread_id")
	}
	if strings.TrimSpace(e.ReplyTo) != clarificationRequest.MessageID {
		return errors.New("clarification.response reply_to must match clarification.request message_id")
	}

	return nil
}
