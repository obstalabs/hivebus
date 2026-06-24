package conformance

// Exported wire mirror structs for the /v0/agents request payloads. These are
// the CONTRACT a consumer codes against — intentionally decoupled from Hivebus's
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

// InboxQuery is the addressing for GET /v0/agents/sessions/{sessionID}/inbox.
// The inbox takes no body; the session id is a path parameter. The fixture pins
// the addressing contract so a consumer constructs the path identically.
type InboxQuery struct {
	SessionID string `json:"session_id"`
}

// RosterQuery is the addressing for the roster read (GET /v0/agents/sessions),
// with the optional participant-prefix filter.
type RosterQuery struct {
	Prefix string `json:"prefix,omitempty"`
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
		return InboxQuery{SessionID: "nr-session-1"}
	case RouteRoster:
		return RosterQuery{Prefix: "claude/"}
	default:
		return nil
	}
}
