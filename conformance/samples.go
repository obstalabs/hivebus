package conformance

// Exported wire mirror structs for the /v0/agents request and response payloads.
// These are the CONTRACT a consumer codes against — intentionally decoupled from Hivebus's
// internal runtime/store types so that an external consumer imports a stable,
// minimal surface rather than reaching into internal packages. The json tags
// here MUST match the bytes Hivebus actually reads/writes; the golden fixtures
// and Hivebus's own internal-type conformance test pin that they do.

// SessionPayload is the register/heartbeat request body
// (POST /v0/agents/sessions/register and .../heartbeat). Mirrors
// model.AgentSessionPayload.
type SessionPayload struct {
	AgentID           string   `json:"agent_id"`
	InstallationID    string   `json:"installation_id"`
	SessionID         string   `json:"session_id"`
	ParticipantID     string   `json:"participant_id"`
	Capabilities      []string `json:"capabilities,omitempty"`
	Roles             []string `json:"roles,omitempty"`
	AnswerPublicKey   string   `json:"answer_public_key,omitempty"`
	DeliveryMode      string   `json:"delivery_mode"`
	SessionStatus     string   `json:"session_status"`
	LeaseExpiresAt    string   `json:"lease_expires_at"`
	HostAlias         string   `json:"host_alias,omitempty"`
	ReplacesSessionID string   `json:"replaces_session_id,omitempty"`
}

// SendMessageRequest is the POST /v0/agents/messages/send request body.
type SendMessageRequest struct {
	MessageID           string `json:"message_id"`
	SenderSessionID     string `json:"sender_session_id"`
	SenderParticipantID string `json:"sender_participant_id"`
	TargetParticipantID string `json:"target_participant_id"`
	ChannelID           string `json:"channel_id,omitempty"`
	Body                string `json:"body"`
	TTLSeconds          int    `json:"ttl_seconds,omitempty"`
}

// DeliverMessageRequest is the POST /v0/agents/messages/{id}/deliver request body.
type DeliverMessageRequest struct {
	SessionID string `json:"session_id"`
}

// AgentSession is the public roster session shape (GET /v0/agents/sessions).
type AgentSession struct {
	AgentID           string   `json:"agent_id"`
	InstallationID    string   `json:"installation_id"`
	SessionID         string   `json:"session_id"`
	ParticipantID     string   `json:"participant_id"`
	Capabilities      []string `json:"capabilities,omitempty"`
	Roles             []string `json:"roles,omitempty"`
	AnswerPublicKey   string   `json:"answer_public_key,omitempty"`
	DeliveryMode      string   `json:"delivery_mode"`
	SessionStatus     string   `json:"session_status"`
	LeaseExpiresAt    string   `json:"lease_expires_at"`
	HostAlias         string   `json:"host_alias,omitempty"`
	ReplacesSessionID string   `json:"replaces_session_id,omitempty"`
	RegisteredAt      string   `json:"registered_at"`
	LastSeenAt        string   `json:"last_seen_at"`
}

// RosterResponse is the GET /v0/agents/sessions response body.
type RosterResponse struct {
	Status   string         `json:"status"`
	Sessions []AgentSession `json:"sessions"`
}

// InboxResponse is the GET /v0/agents/sessions/{sessionID}/inbox response body
// pinned by WO-159.
type InboxResponse struct {
	Status   string         `json:"status"`
	Session  AgentSession   `json:"session"`
	Messages []MessageEntry `json:"messages"`
}

// MessageEntry is one inbox message with its delivery event history.
type MessageEntry struct {
	Message AgentMessage   `json:"message"`
	Events  []MessageEvent `json:"events"`
}

// AgentMessage is the public queued-message shape returned in inbox responses.
type AgentMessage struct {
	MessageID             string `json:"message_id"`
	SenderSessionID       string `json:"sender_session_id"`
	SenderParticipantID   string `json:"sender_participant_id"`
	TargetParticipantID   string `json:"target_participant_id"`
	TargetAgentID         string `json:"target_agent_id,omitempty"`
	TargetAnswerPublicKey string `json:"target_answer_public_key,omitempty"`
	ChannelID             string `json:"channel_id,omitempty"`
	Body                  string `json:"body"`
	CreatedAt             string `json:"created_at"`
	ExpiresAt             string `json:"expires_at"`
	State                 string `json:"state"`
	DeliveredSessionID    string `json:"delivered_session_id,omitempty"`
	DeliveredAt           string `json:"delivered_at,omitempty"`
	Reason                string `json:"reason,omitempty"`
}

// MessageEvent is one delivery-state transition in an inbox message history.
type MessageEvent struct {
	Sequence        int64  `json:"sequence"`
	MessageID       string `json:"message_id"`
	EventAt         string `json:"event_at"`
	State           string `json:"state"`
	TargetSessionID string `json:"target_session_id,omitempty"`
	Reason          string `json:"reason,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	QueuePosition   int    `json:"queue_position,omitempty"`
}

// Sample returns the canonical sample value for a route. These values are
// deterministic (no clocks, no randomness) so the golden fixtures are stable.
func Sample(route Route) any {
	switch route {
	case RouteSessionRegister, RouteSessionHeartbeat:
		return SessionPayload{
			AgentID:         "claude/hivebus",
			InstallationID:  "install-1",
			SessionID:       "nr-session-1",
			ParticipantID:   "nr-participant-1",
			Capabilities:    []string{"repo_status", "canonical_worktree_status"},
			Roles:           []string{"worker"},
			AnswerPublicKey: "ed25519:AAAA",
			DeliveryMode:    "queued_delivery",
			SessionStatus:   "online",
			LeaseExpiresAt:  "2026-01-01T00:02:00Z",
			HostAlias:       "host-a",
		}
	case RouteMessageSend:
		return SendMessageRequest{
			MessageID:           "hbm-1",
			SenderSessionID:     "nr-session-1",
			SenderParticipantID: "nr-participant-1",
			TargetParticipantID: "nr-participant-2",
			Body:                "what work order are you on?",
			TTLSeconds:          600,
		}
	case RouteMessageDeliver:
		return DeliverMessageRequest{SessionID: "nr-session-2"}
	case RouteInbox:
		return InboxResponse{
			Status:  "ok",
			Session: sampleAgentSession(),
			Messages: []MessageEntry{
				{
					Message: AgentMessage{
						MessageID:             "hbm-1",
						SenderSessionID:       "nr-session-2",
						SenderParticipantID:   "nr-participant-2",
						TargetParticipantID:   "nr-participant-1",
						TargetAgentID:         "claude/hivebus",
						TargetAnswerPublicKey: "ed25519:AAAA",
						Body:                  "what work order are you on?",
						CreatedAt:             "2026-01-01T00:00:30Z",
						ExpiresAt:             "2026-01-01T00:10:30Z",
						State:                 "queued",
						DeliveredAt:           "0001-01-01T00:00:00Z",
					},
					Events: []MessageEvent{
						{
							Sequence:        1,
							MessageID:       "hbm-1",
							EventAt:         "2026-01-01T00:00:30Z",
							State:           "queued",
							TargetSessionID: "nr-session-1",
							ExpiresAt:       "2026-01-01T00:10:30Z",
							QueuePosition:   1,
						},
					},
				},
			},
		}
	case RouteRoster:
		return RosterResponse{
			Status:   "ok",
			Sessions: []AgentSession{sampleAgentSession()},
		}
	default:
		return nil
	}
}

func sampleAgentSession() AgentSession {
	return AgentSession{
		AgentID:         "claude/hivebus",
		InstallationID:  "install-1",
		SessionID:       "nr-session-1",
		ParticipantID:   "nr-participant-1",
		Capabilities:    []string{"repo_status", "canonical_worktree_status"},
		Roles:           []string{"worker"},
		AnswerPublicKey: "ed25519:AAAA",
		DeliveryMode:    "queued_delivery",
		SessionStatus:   "online",
		LeaseExpiresAt:  "2026-01-01T00:02:00Z",
		HostAlias:       "host-a",
		RegisteredAt:    "2026-01-01T00:00:00Z",
		LastSeenAt:      "2026-01-01T00:01:00Z",
	}
}
