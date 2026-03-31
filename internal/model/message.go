package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// MessageType identifies the intent of an envelope.
type MessageType string

const (
	MessageTypeTaskRequest      MessageType = "task.request"
	MessageTypeTaskAccepted     MessageType = "task.accepted"
	MessageTypeTaskResultPart   MessageType = "task.result.partial"
	MessageTypeTaskResultFinal  MessageType = "task.result.final"
	MessageTypeClarifyRequest   MessageType = "clarification.request"
	MessageTypeClarifyResponse  MessageType = "clarification.response"
	MessageTypeEvidenceCaptured MessageType = "evidence.captured"
	MessageTypeDiagnosisPropose MessageType = "diagnosis.proposed"
	MessageTypeWorkOrderCreate  MessageType = "work_order.create"
	MessageTypeTaskCancel       MessageType = "task.cancel"
)

var validMessageTypes = []MessageType{
	MessageTypeTaskRequest,
	MessageTypeTaskAccepted,
	MessageTypeTaskResultPart,
	MessageTypeTaskResultFinal,
	MessageTypeClarifyRequest,
	MessageTypeClarifyResponse,
	MessageTypeEvidenceCaptured,
	MessageTypeDiagnosisPropose,
	MessageTypeWorkOrderCreate,
	MessageTypeTaskCancel,
}

// Trace captures provenance for audit and replay.
type Trace struct {
	CorrelationID string `json:"correlation_id"`
	SpanID        string `json:"span_id,omitempty"`
	Model         string `json:"model,omitempty"`
	Verified      bool   `json:"verified"`
}

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
	ArtifactID string `json:"artifact_id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	URI        string `json:"uri"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
	Redacted   bool   `json:"redacted"`
}

// Validate applies structural checks that must hold before any routing.
func (e Envelope) Validate() error {
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
	}

	return nil
}
