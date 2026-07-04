package runtime

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
	"github.com/obstalabs/hivebus/internal/store"
)

const defaultAgentMessageTTL = time.Hour

type registerAgentSessionResponse struct {
	Status  string             `json:"status"`
	Session store.AgentSession `json:"session"`
}

type sendAgentMessageRequest struct {
	MessageID           string `json:"message_id"`
	SenderSessionID     string `json:"sender_session_id"`
	SenderParticipantID string `json:"sender_participant_id"`
	TargetParticipantID string `json:"target_participant_id"`
	TargetHandle        string `json:"target_handle,omitempty"` // WO-174: preferred stable route key; resolved server-side.
	Repository          string `json:"repository,omitempty"`    // WO-174: optional scope for role handles.
	ChannelID           string `json:"channel_id,omitempty"`
	Body                string `json:"body"`
	TTLSeconds          int    `json:"ttl_seconds,omitempty"`
}

type sendAgentMessageResponse struct {
	Status  string                       `json:"status"`
	Message store.AgentMessage           `json:"message"`
	Receipt model.DeliveryReceiptPayload `json:"receipt"`
}

type upsertChannelResponse struct {
	Status  string        `json:"status"`
	Channel model.Channel `json:"channel"`
}

type inboxResponse struct {
	Status   string                     `json:"status"`
	Session  store.AgentSession         `json:"session"`
	Messages []store.AgentMessageRecord `json:"messages"`
}

type deliverAgentMessageRequest struct {
	SessionID string `json:"session_id"`
}

type deliverAgentMessageResponse struct {
	Status  string                       `json:"status"`
	Message store.AgentMessage           `json:"message"`
	Receipt model.DeliveryReceiptPayload `json:"receipt"`
}

func (s *server) handleRegisterAgentSession(w http.ResponseWriter, r *http.Request) {
	var payload model.AgentSessionPayload
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	session, err := s.store.RegisterAgentSession(
		r.Context(),
		payload,
		model.MessageTypeAgentSessionRegistered,
		s.currentTime(),
	)
	switch {
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusCreated, registerAgentSessionResponse{
			Status:  "registered",
			Session: session,
		})
	}
}

func (s *server) handleHeartbeatAgentSession(w http.ResponseWriter, r *http.Request) {
	var payload model.AgentSessionPayload
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	session, err := s.store.RegisterAgentSession(
		r.Context(),
		payload,
		model.MessageTypeAgentSessionHeartbeat,
		s.currentTime(),
	)
	switch {
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, registerAgentSessionResponse{
			Status:  "heartbeat",
			Session: session,
		})
	}
}

func (s *server) handleSendAgentMessage(w http.ResponseWriter, r *http.Request) {
	var request sendAgentMessageRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	ttl := defaultAgentMessageTTL
	if request.TTLSeconds > 0 {
		ttl = time.Duration(request.TTLSeconds) * time.Second
	}

	now := s.currentTime()
	targetHandle := strings.TrimSpace(request.TargetHandle)
	targetRepository := strings.TrimSpace(request.Repository)
	targetParticipantID := request.TargetParticipantID
	var resolution *store.ResolvedTargetHandle
	// WO-174: resolve a stable handle to the freshest-live participant only on a
	// DIRECTED send (no channel_id). A channel/broadcast send never rebinds its
	// route by handle — a stray target_handle there is metadata, not routing
	// authority (mirrors the NR directed-only boundary; OSS has no ask channel).
	if targetHandle != "" && strings.TrimSpace(request.ChannelID) == "" {
		resolved, err := s.store.ResolveTargetHandle(
			r.Context(), targetHandle, targetRepository, now,
		)
		if err != nil {
			writeTargetHandleError(w, err)
			return
		}
		targetParticipantID = resolved.ParticipantID
		resolution = &resolved
	}

	input := store.AgentMessageInput{
		MessageID:           request.MessageID,
		SenderSessionID:     request.SenderSessionID,
		SenderParticipantID: request.SenderParticipantID,
		TargetParticipantID: targetParticipantID,
		ChannelID:           request.ChannelID,
		Body:                request.Body,
		TTL:                 ttl,
	}
	if resolution != nil {
		// WO-175: persist resolution provenance with the queued message, not only
		// on the immediate HTTP response.
		input.TargetHandle = targetHandle
		input.Repository = targetRepository
		input.ResolvedTargetParticipantID = resolution.ParticipantID
		input.ResolvedTargetSessionID = resolution.SessionID
		input.ResolutionMode = store.ResolutionModeServerSideHandle
		if ignored := strings.TrimSpace(request.TargetParticipantID); ignored != "" && ignored != resolution.ParticipantID {
			input.IgnoredTargetParticipantID = ignored
		}
	}

	record, err := s.store.QueueAgentMessage(r.Context(), input, now)
	switch {
	case errors.Is(err, store.ErrChannelNotFound):
		writeError(w, http.StatusNotFound, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		receipt := queuedReceipt(record)
		writeJSON(w, http.StatusCreated, sendAgentMessageResponse{
			Status:  "queued",
			Message: record.Message,
			Receipt: receipt,
		})
	}
}

// writeTargetHandleError maps a WO-174 resolution failure to its wire code.
func writeTargetHandleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrTargetHandleNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrTargetHandleNotLive):
		writeError(w, http.StatusConflict, err)
	case errors.Is(err, store.ErrTargetHandleAmbiguous):
		writeError(w, http.StatusConflict, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}

func (s *server) handleUpsertChannel(w http.ResponseWriter, r *http.Request) {
	var channel model.Channel
	if err := decodeJSON(r.Body, &channel); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	now := s.currentTime()
	if channel.CreatedAt.IsZero() {
		channel.CreatedAt = now
	}
	if channel.UpdatedAt.IsZero() || channel.UpdatedAt.Before(channel.CreatedAt) {
		channel.UpdatedAt = now
	}

	stored, err := s.store.UpsertChannel(r.Context(), channel)
	switch {
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusCreated, upsertChannelResponse{
			Status:  "stored",
			Channel: stored,
		})
	}
}

func (s *server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	channel, err := s.store.LoadChannel(r.Context(), r.PathValue("channelID"))
	switch {
	case errors.Is(err, store.ErrChannelNotFound):
		writeError(w, http.StatusNotFound, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, channel)
	}
}

func (s *server) handlePeekAgentInbox(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	limit := 0
	session, messages, err := s.store.PeekAgentInbox(r.Context(), sessionID, s.currentTime(), limit)
	switch {
	case errors.Is(err, store.ErrAgentSessionNotFound):
		writeError(w, http.StatusNotFound, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, inboxResponse{
			Status:   "ok",
			Session:  session,
			Messages: messages,
		})
	}
}

func (s *server) handleDeliverAgentMessage(w http.ResponseWriter, r *http.Request) {
	var request deliverAgentMessageRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	record, err := s.store.DeliverAgentMessage(
		r.Context(),
		r.PathValue("messageID"),
		request.SessionID,
		s.currentTime(),
	)
	switch {
	case errors.Is(err, store.ErrAgentSessionNotFound), errors.Is(err, store.ErrAgentMessageNotFound):
		writeError(w, http.StatusNotFound, err)
	case errors.Is(err, store.ErrAgentMessageFinalized), errors.Is(err, store.ErrAgentMessageWrongQueue):
		writeError(w, http.StatusConflict, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, deliverAgentMessageResponse{
			Status:  "delivered",
			Message: record.Message,
			Receipt: deliveredReceipt(record),
		})
	}
}

func (s *server) handleGetAgentMessage(w http.ResponseWriter, r *http.Request) {
	record, err := s.store.GetAgentMessage(r.Context(), r.PathValue("messageID"), s.currentTime())
	switch {
	case errors.Is(err, store.ErrAgentMessageNotFound):
		writeError(w, http.StatusNotFound, err)
	case err != nil && isInputError(err):
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err)
	default:
		writeJSON(w, http.StatusOK, record)
	}
}

func queuedReceipt(record store.AgentMessageRecord) model.DeliveryReceiptPayload {
	queuePosition := 0
	for _, event := range record.Events {
		if event.State == model.DeliveryReceiptQueued {
			queuePosition = event.QueuePosition
		}
	}

	return model.DeliveryReceiptPayload{
		TargetParticipantID: record.Message.TargetParticipantID,
		TargetAgentID:       record.Message.TargetAgentID,
		State:               model.DeliveryReceiptQueued,
		ExpiresAt:           record.Message.ExpiresAt.Format(time.RFC3339),
		QueuePosition:       queuePosition,
	}
}

func deliveredReceipt(record store.AgentMessageRecord) model.DeliveryReceiptPayload {
	return model.DeliveryReceiptPayload{
		TargetParticipantID: record.Message.TargetParticipantID,
		TargetAgentID:       record.Message.TargetAgentID,
		TargetSessionID:     record.Message.DeliveredSessionID,
		State:               model.DeliveryReceiptDelivered,
	}
}
