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

	record, err := s.store.QueueAgentMessage(r.Context(), store.AgentMessageInput{
		MessageID:           request.MessageID,
		SenderSessionID:     request.SenderSessionID,
		SenderParticipantID: request.SenderParticipantID,
		TargetParticipantID: request.TargetParticipantID,
		ChannelID:           request.ChannelID,
		Body:                request.Body,
		TTL:                 ttl,
	}, s.currentTime())
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
