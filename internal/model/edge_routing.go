package model

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type AgentDeliveryMode string

const (
	AgentDeliveryOutboundOnly AgentDeliveryMode = "outbound_only"
	AgentDeliveryQueued       AgentDeliveryMode = "queued_delivery"
)

var validAgentDeliveryModes = []AgentDeliveryMode{
	AgentDeliveryOutboundOnly,
	AgentDeliveryQueued,
}

type AgentSessionStatus string

const (
	AgentSessionOnline   AgentSessionStatus = "online"
	AgentSessionOffline  AgentSessionStatus = "offline"
	AgentSessionReplaced AgentSessionStatus = "replaced"
)

var validAgentSessionStatuses = []AgentSessionStatus{
	AgentSessionOnline,
	AgentSessionOffline,
	AgentSessionReplaced,
}

type DeliveryReceiptState string

const (
	DeliveryReceiptQueued    DeliveryReceiptState = "queued"
	DeliveryReceiptDelivered DeliveryReceiptState = "delivered"
	DeliveryReceiptExpired   DeliveryReceiptState = "expired"
	DeliveryReceiptRefused   DeliveryReceiptState = "refused"
)

var validDeliveryReceiptStates = []DeliveryReceiptState{
	DeliveryReceiptQueued,
	DeliveryReceiptDelivered,
	DeliveryReceiptExpired,
	DeliveryReceiptRefused,
}

// AgentSessionPayload declares how Hivebus reaches an edge agent by stable
// participant identity rather than callback URLs or host IPs.
type AgentSessionPayload struct {
	AgentID           string             `json:"agent_id"`
	InstallationID    string             `json:"installation_id"`
	SessionID         string             `json:"session_id"`
	ParticipantID     string             `json:"participant_id"`
	Capabilities      []string           `json:"capabilities,omitempty"`
	Roles             []string           `json:"roles,omitempty"`
	AnswerPublicKey   string             `json:"answer_public_key,omitempty"` // WO-122: discovery key only; askers pin trust locally.
	DeliveryMode      AgentDeliveryMode  `json:"delivery_mode"`
	SessionStatus     AgentSessionStatus `json:"session_status"`
	LeaseExpiresAt    string             `json:"lease_expires_at"`
	HostAlias         string             `json:"host_alias,omitempty"`
	ReplacesSessionID string             `json:"replaces_session_id,omitempty"`
}

func (p AgentSessionPayload) Validate(eventType MessageType) error {
	switch {
	case strings.TrimSpace(p.AgentID) == "":
		return errors.New("agent_id is required")
	case strings.TrimSpace(p.InstallationID) == "":
		return errors.New("installation_id is required")
	case strings.TrimSpace(p.SessionID) == "":
		return errors.New("session_id is required")
	case strings.TrimSpace(p.ParticipantID) == "":
		return errors.New("participant_id is required")
	case !slices.Contains(validAgentDeliveryModes, p.DeliveryMode):
		return fmt.Errorf("unsupported delivery_mode %q", p.DeliveryMode)
	case !slices.Contains(validAgentSessionStatuses, p.SessionStatus):
		return fmt.Errorf("unsupported session_status %q", p.SessionStatus)
	case strings.TrimSpace(p.LeaseExpiresAt) == "":
		return errors.New("lease_expires_at is required")
	}

	if _, err := time.Parse(time.RFC3339, p.LeaseExpiresAt); err != nil {
		return fmt.Errorf("lease_expires_at must be RFC3339: %w", err)
	}
	if strings.TrimSpace(p.AnswerPublicKey) != "" {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(p.AnswerPublicKey))
		if err != nil {
			return fmt.Errorf("answer_public_key must be base64 ed25519: %w", err)
		}
		if len(decoded) != ed25519.PublicKeySize {
			return fmt.Errorf("answer_public_key size = %d, want %d", len(decoded), ed25519.PublicKeySize)
		}
	}

	seenCapabilities := make(map[string]struct{}, len(p.Capabilities))
	for _, capability := range p.Capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			return errors.New("capabilities contains an empty capability")
		}
		if _, exists := seenCapabilities[capability]; exists {
			return fmt.Errorf("duplicate capability %q", capability)
		}
		seenCapabilities[capability] = struct{}{}
	}

	seenRoles := make(map[string]struct{}, len(p.Roles))
	for _, role := range p.Roles {
		role = strings.TrimSpace(role)
		if role == "" {
			return errors.New("roles contains an empty role")
		}
		if _, exists := seenRoles[role]; exists {
			return fmt.Errorf("duplicate role %q", role)
		}
		seenRoles[role] = struct{}{}
	}

	switch eventType {
	case MessageTypeAgentSessionRegistered:
		if strings.TrimSpace(p.ReplacesSessionID) != "" && p.SessionStatus != AgentSessionReplaced {
			return errors.New("replaces_session_id requires session_status replaced")
		}
	case MessageTypeAgentSessionHeartbeat:
		if strings.TrimSpace(p.ReplacesSessionID) != "" {
			return errors.New("agent.session.heartbeat must not set replaces_session_id")
		}
	default:
		return fmt.Errorf("unsupported edge session event %q", eventType)
	}

	return nil
}

// DeliveryReceiptPayload makes offline queueing and final delivery outcomes
// explicit instead of inferred from missing follow-up text.
type DeliveryReceiptPayload struct {
	TargetParticipantID string               `json:"target_participant_id"`
	TargetAgentID       string               `json:"target_agent_id"`
	TargetSessionID     string               `json:"target_session_id,omitempty"`
	State               DeliveryReceiptState `json:"state"`
	ExpiresAt           string               `json:"expires_at,omitempty"`
	QueuePosition       int                  `json:"queue_position,omitempty"`
	Reason              string               `json:"reason,omitempty"`
}

func (p DeliveryReceiptPayload) Validate() error {
	switch {
	case strings.TrimSpace(p.TargetParticipantID) == "":
		return errors.New("target_participant_id is required")
	case strings.TrimSpace(p.TargetAgentID) == "":
		return errors.New("target_agent_id is required")
	case !slices.Contains(validDeliveryReceiptStates, p.State):
		return fmt.Errorf("unsupported state %q", p.State)
	}

	switch p.State {
	case DeliveryReceiptQueued:
		if strings.TrimSpace(p.ExpiresAt) == "" {
			return errors.New("queued delivery receipt requires expires_at")
		}
		if _, err := time.Parse(time.RFC3339, p.ExpiresAt); err != nil {
			return fmt.Errorf("expires_at must be RFC3339: %w", err)
		}
		if p.QueuePosition <= 0 {
			return errors.New("queued delivery receipt requires positive queue_position")
		}
		if strings.TrimSpace(p.TargetSessionID) != "" {
			return errors.New("queued delivery receipt must not set target_session_id")
		}
	case DeliveryReceiptDelivered:
		if strings.TrimSpace(p.TargetSessionID) == "" {
			return errors.New("delivered delivery receipt requires target_session_id")
		}
		if strings.TrimSpace(p.ExpiresAt) != "" {
			return errors.New("delivered delivery receipt must not set expires_at")
		}
		if p.QueuePosition != 0 {
			return errors.New("delivered delivery receipt must not set queue_position")
		}
		if strings.TrimSpace(p.Reason) != "" {
			return errors.New("delivered delivery receipt must not set reason")
		}
	case DeliveryReceiptExpired, DeliveryReceiptRefused:
		if strings.TrimSpace(p.Reason) == "" {
			return fmt.Errorf("%s delivery receipt requires reason", p.State)
		}
		if p.QueuePosition != 0 {
			return fmt.Errorf("%s delivery receipt must not set queue_position", p.State)
		}
	default:
		return fmt.Errorf("unsupported state %q", p.State)
	}

	return nil
}

func (e Envelope) ValidateEdgeRouting() error {
	if err := e.Validate(); err != nil {
		return err
	}

	switch e.Type {
	case MessageTypeAgentSessionRegistered, MessageTypeAgentSessionHeartbeat:
		var payload AgentSessionPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("decode edge session payload: %w", err)
		}
		return payload.Validate(e.Type)
	case MessageTypeAgentDeliveryReceipt:
		var payload DeliveryReceiptPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return fmt.Errorf("decode delivery receipt payload: %w", err)
		}
		return payload.Validate()
	default:
		return fmt.Errorf("expected edge routing event, got %q", e.Type)
	}
}
