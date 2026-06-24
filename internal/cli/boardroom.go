package cli

import (
	"bytes"
	"context"
	cryptoRand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
	"github.com/spf13/cobra"
)

const (
	boardroomMaxMessageBytes = 8192 // WO-153: keep generic boardroom messages bounded.
	boardroomRandomBytes     = 16
	defaultBoardroomTTL      = time.Hour
	defaultBoardroomLease    = 24 * time.Hour
	defaultListenTimeout     = 30 * time.Second
	defaultListenPoll        = 500 * time.Millisecond
)

var (
	boardroomHTTPClient askHTTPDoer = http.DefaultClient
	boardroomRandom     io.Reader   = cryptoRand.Reader
	boardroomNow                    = func() time.Time { return time.Now().UTC() }
	boardroomSleep                  = time.Sleep
)

type boardroomOptions struct {
	serverURL      string
	sessionID      string
	installationID string
	participantID  string
	from           string
	to             string
	operatorToken  string
	workerToken    string
	insecure       bool
	messageID      string
	ttl            time.Duration
	leaseDuration  time.Duration
	ack            bool
	timeout        time.Duration
	pollInterval   time.Duration
}

type boardroomSendRequest struct {
	MessageID           string `json:"message_id"`
	SenderSessionID     string `json:"sender_session_id"`
	SenderParticipantID string `json:"sender_participant_id"`
	TargetParticipantID string `json:"target_participant_id"`
	Body                string `json:"body"`
	TTLSeconds          int    `json:"ttl_seconds,omitempty"`
}

type boardroomSendOutput struct {
	Status              string `json:"status"`
	MessageID           string `json:"message_id"`
	SenderSessionID     string `json:"sender_session_id"`
	SenderParticipantID string `json:"sender_participant_id"`
	TargetParticipantID string `json:"target_participant_id"`
	TargetAgentID       string `json:"target_agent_id,omitempty"`
}

type boardroomSendResponse struct {
	Status  string                `json:"status"`
	Message boardroomAgentMessage `json:"message"`
}

type boardroomInboxResponse struct {
	Status   string                   `json:"status"`
	Session  boardroomAgentSession    `json:"session"`
	Messages []boardroomMessageRecord `json:"messages"`
}

type boardroomInboxOutput struct {
	Status    string                   `json:"status"`
	Session   boardroomAgentSession    `json:"session"`
	Messages  []boardroomMessageRecord `json:"messages"`
	Delivered []string                 `json:"delivered,omitempty"`
}

type boardroomListenOutput struct {
	Status   string                   `json:"status"`
	Session  boardroomAgentSession    `json:"session,omitempty"`
	Messages []boardroomMessageRecord `json:"messages,omitempty"`
	Error    string                   `json:"error,omitempty"`
}

type boardroomAgentSession struct {
	AgentID        string `json:"agent_id"`
	InstallationID string `json:"installation_id"`
	SessionID      string `json:"session_id"`
	ParticipantID  string `json:"participant_id"`
	DeliveryMode   string `json:"delivery_mode"`
	SessionStatus  string `json:"session_status"`
	LeaseExpiresAt string `json:"lease_expires_at"`
	RegisteredAt   string `json:"registered_at"`
	LastSeenAt     string `json:"last_seen_at"`
}

type boardroomMessageRecord struct {
	Message boardroomAgentMessage `json:"message"`
}

type boardroomAgentMessage struct {
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
}

func newSayCommand() *cobra.Command {
	options := boardroomOptions{
		installationID: "local",
		ttl:            defaultBoardroomTTL,
		leaseDuration:  defaultBoardroomLease,
	}

	cmd := &cobra.Command{
		Use:          "say --server <url> --from <participant> --to <participant> [message]",
		Short:        "Send a bounded text message through Hivebus",
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			options.normalize()
			body, err := readBoardroomMessage(args, cmd.InOrStdin())
			if err != nil {
				return err
			}
			return runBoardroomSay(cmd.Context(), cmd.OutOrStdout(), options, body)
		},
	}

	cmd.Flags().StringVar(&options.serverURL, "server", "", "hivebus server URL")
	cmd.Flags().StringVar(&options.from, "from", "", "sender participant identity")
	cmd.Flags().StringVar(&options.to, "to", "", "target participant identity")
	cmd.Flags().StringVar(&options.sessionID, "session-id", "", "sender runtime session ID")
	cmd.Flags().StringVar(&options.installationID, "installation-id", "local", "installation identity")
	cmd.Flags().StringVar(&options.messageID, "message-id", "", "explicit message ID")
	cmd.Flags().DurationVar(&options.ttl, "ttl", defaultBoardroomTTL, "message time to live")
	cmd.Flags().DurationVar(&options.leaseDuration, "lease-duration", defaultBoardroomLease, "sender session lease duration")
	cmd.Flags().StringVar(&options.operatorToken, "operator-token", "", "operator auth token for send")
	cmd.Flags().StringVar(&options.workerToken, "worker-token", "", "worker auth token for session registration")
	cmd.Flags().BoolVar(&options.insecure, "insecure", false, "allow tokenless calls against a serve --auth-disabled server")
	return cmd
}

func newInboxCommand() *cobra.Command {
	options := boardroomOptions{
		installationID: "local",
		leaseDuration:  defaultBoardroomLease,
	}

	cmd := &cobra.Command{
		Use:          "inbox --server <url> --session-id <id>",
		Short:        "Read a Hivebus participant inbox",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			options.normalize()
			return runBoardroomInbox(cmd.Context(), cmd.OutOrStdout(), options)
		},
	}

	addBoardroomInboxFlags(cmd, &options)
	return cmd
}

func newListenCommand() *cobra.Command {
	options := boardroomOptions{
		installationID: "local",
		leaseDuration:  defaultBoardroomLease,
		timeout:        defaultListenTimeout,
		pollInterval:   defaultListenPoll,
	}

	cmd := &cobra.Command{
		Use:          "listen --server <url> --session-id <id>",
		Short:        "Poll a Hivebus participant inbox until a message or timeout",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			options.normalize()
			return runBoardroomListen(cmd.Context(), cmd.OutOrStdout(), options)
		},
	}

	addBoardroomInboxFlags(cmd, &options)
	cmd.Flags().DurationVar(&options.timeout, "timeout", defaultListenTimeout, "maximum time to wait")
	cmd.Flags().DurationVar(&options.pollInterval, "poll-interval", defaultListenPoll, "poll interval")
	return cmd
}

func addBoardroomInboxFlags(cmd *cobra.Command, options *boardroomOptions) {
	cmd.Flags().StringVar(&options.serverURL, "server", "", "hivebus server URL")
	cmd.Flags().StringVar(&options.sessionID, "session-id", "", "runtime session ID")
	cmd.Flags().StringVar(&options.participantID, "participant", "", "participant identity to register before reading")
	cmd.Flags().StringVar(&options.installationID, "installation-id", "local", "installation identity")
	cmd.Flags().DurationVar(&options.leaseDuration, "lease-duration", defaultBoardroomLease, "session lease duration")
	cmd.Flags().StringVar(&options.workerToken, "worker-token", "", "worker auth token for inbox and delivery")
	cmd.Flags().BoolVar(&options.insecure, "insecure", false, "allow tokenless calls against a serve --auth-disabled server")
	cmd.Flags().BoolVar(&options.ack, "ack", false, "mark returned messages delivered")
}

func runBoardroomSay(ctx context.Context, out io.Writer, options boardroomOptions, body string) error {
	if err := options.validateSay(); err != nil {
		return err
	}
	if err := boardroomRegister(ctx, options, options.from); err != nil {
		return err
	}
	messageID := options.messageID
	if messageID == "" {
		token, err := boardroomRandomToken(boardroomRandom)
		if err != nil {
			return err
		}
		messageID = "hbm-" + token
	}

	request := boardroomSendRequest{
		MessageID:           messageID,
		SenderSessionID:     options.sessionID,
		SenderParticipantID: options.from,
		TargetParticipantID: options.to,
		Body:                body,
	}
	if options.ttl > 0 {
		request.TTLSeconds = boardroomTTLSeconds(options.ttl)
	}

	var response boardroomSendResponse
	if err := boardroomRequest(ctx, options, http.MethodPost, "/v0/agents/messages/send", options.operatorToken, request, &response); err != nil {
		return err
	}
	return writeJSON(out, boardroomSendOutput{
		Status:              firstNonEmpty(response.Status, "queued"),
		MessageID:           firstNonEmpty(response.Message.MessageID, messageID),
		SenderSessionID:     firstNonEmpty(response.Message.SenderSessionID, options.sessionID),
		SenderParticipantID: firstNonEmpty(response.Message.SenderParticipantID, options.from),
		TargetParticipantID: firstNonEmpty(response.Message.TargetParticipantID, options.to),
		TargetAgentID:       response.Message.TargetAgentID,
	})
}

func runBoardroomInbox(ctx context.Context, out io.Writer, options boardroomOptions) error {
	if err := options.validateInbox(); err != nil {
		return err
	}
	response, delivered, err := boardroomFetchAndMaybeAck(ctx, options)
	if err != nil {
		return err
	}
	return writeJSON(out, boardroomInboxOutput{
		Status:    "ok",
		Session:   response.Session,
		Messages:  response.Messages,
		Delivered: delivered,
	})
}

func runBoardroomListen(ctx context.Context, out io.Writer, options boardroomOptions) error {
	if err := options.validateListen(); err != nil {
		return err
	}
	deadline := boardroomNow().Add(options.timeout)
	for {
		response, delivered, err := boardroomFetchAndMaybeAck(ctx, options)
		if err != nil {
			_ = writeJSON(out, boardroomListenOutput{Status: "bus_down", Error: err.Error()})
			return err
		}
		if len(response.Messages) > 0 {
			_ = delivered
			return writeJSON(out, boardroomListenOutput{
				Status:   "message",
				Session:  response.Session,
				Messages: response.Messages,
			})
		}
		if !boardroomNow().Before(deadline) || options.timeout <= 0 || options.pollInterval <= 0 {
			return writeJSON(out, boardroomListenOutput{Status: "timeout", Session: response.Session})
		}
		boardroomSleep(options.pollInterval)
	}
}

func boardroomFetchAndMaybeAck(ctx context.Context, options boardroomOptions) (boardroomInboxResponse, []string, error) {
	if strings.TrimSpace(options.participantID) != "" {
		if err := boardroomRegister(ctx, options, options.participantID); err != nil {
			return boardroomInboxResponse{}, nil, err
		}
	}
	path := "/v0/agents/sessions/" + url.PathEscape(options.sessionID) + "/inbox"
	var response boardroomInboxResponse
	if err := boardroomRequest(ctx, options, http.MethodGet, path, options.workerToken, nil, &response); err != nil {
		return boardroomInboxResponse{}, nil, err
	}
	if response.Status == "" {
		response.Status = "ok"
	}
	if !options.ack || len(response.Messages) == 0 {
		return response, nil, nil
	}
	delivered := make([]string, 0, len(response.Messages))
	for _, record := range response.Messages {
		messageID := strings.TrimSpace(record.Message.MessageID)
		if messageID == "" {
			continue
		}
		if err := boardroomDeliver(ctx, options, messageID); err != nil {
			return boardroomInboxResponse{}, delivered, err
		}
		delivered = append(delivered, messageID)
	}
	return response, delivered, nil
}

func boardroomRegister(ctx context.Context, options boardroomOptions, participantID string) error {
	participantID = strings.TrimSpace(participantID)
	if participantID == "" {
		return errors.New("participant identity is required for registration")
	}
	payload := model.AgentSessionPayload{
		AgentID:        participantID,
		InstallationID: options.installationID,
		SessionID:      options.sessionID,
		ParticipantID:  participantID,
		Capabilities:   []string{"boardroom_message"},
		Roles:          []string{"participant"},
		DeliveryMode:   model.AgentDeliveryQueued,
		SessionStatus:  model.AgentSessionOnline,
		LeaseExpiresAt: boardroomNow().Add(options.leaseDuration).UTC().Format(time.RFC3339),
	}
	return boardroomRequest(ctx, options, http.MethodPost, "/v0/agents/sessions/register", options.workerToken, payload, nil)
}

func boardroomDeliver(ctx context.Context, options boardroomOptions, messageID string) error {
	payload := struct {
		SessionID string `json:"session_id"`
	}{SessionID: options.sessionID}
	path := "/v0/agents/messages/" + url.PathEscape(messageID) + "/deliver"
	return boardroomRequest(ctx, options, http.MethodPost, path, options.workerToken, payload, nil)
}

func boardroomRequest(
	ctx context.Context,
	options boardroomOptions,
	method string,
	path string,
	token string,
	payload any,
	responsePayload any,
) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	endpoint, err := askServerEndpoint(options.serverURL, path)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(token) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	response, err := boardroomHTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("boardroom %s %s: %w", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("boardroom %s %s failed: status %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if responsePayload != nil && strings.TrimSpace(string(responseBody)) != "" {
		if err := json.Unmarshal(responseBody, responsePayload); err != nil {
			return fmt.Errorf("boardroom %s %s response must be json: %w", method, path, err)
		}
	}
	return nil
}

func readBoardroomMessage(args []string, reader io.Reader) (string, error) {
	if len(args) > 0 {
		message := strings.TrimSpace(strings.Join(args, " "))
		if message == "" {
			return "", errors.New("message is required")
		}
		if len(message) > boardroomMaxMessageBytes {
			return "", fmt.Errorf("message must be at most %d bytes", boardroomMaxMessageBytes)
		}
		return message, nil
	}
	data, err := io.ReadAll(io.LimitReader(reader, boardroomMaxMessageBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > boardroomMaxMessageBytes {
		return "", fmt.Errorf("message must be at most %d bytes", boardroomMaxMessageBytes)
	}
	message := strings.TrimSpace(string(data))
	if message == "" {
		return "", errors.New("message is required on stdin or argv")
	}
	return message, nil
}

func (options *boardroomOptions) normalize() {
	options.serverURL = strings.TrimRight(strings.TrimSpace(options.serverURL), "/")
	options.sessionID = strings.TrimSpace(options.sessionID)
	options.installationID = strings.TrimSpace(options.installationID)
	options.participantID = strings.TrimSpace(options.participantID)
	options.from = strings.TrimSpace(options.from)
	options.to = strings.TrimSpace(options.to)
	options.operatorToken = strings.TrimSpace(options.operatorToken)
	options.workerToken = strings.TrimSpace(options.workerToken)
	options.messageID = strings.TrimSpace(options.messageID)
	if options.installationID == "" {
		options.installationID = "local"
	}
}

func (options boardroomOptions) validateSay() error {
	if err := options.validateCommon(); err != nil {
		return err
	}
	switch {
	case options.from == "":
		return errors.New("from is required")
	case options.to == "":
		return errors.New("to is required")
	case options.ttl < 0:
		return errors.New("ttl must be non-negative")
	case !options.insecure && options.operatorToken == "":
		return errors.New("operator-token is required for say (or pass --insecure for a serve --auth-disabled server)")
	case !options.insecure && options.workerToken == "":
		return errors.New("worker-token is required for sender registration (or pass --insecure for a serve --auth-disabled server)")
	default:
		return nil
	}
}

func (options boardroomOptions) validateInbox() error {
	return options.validateCommon()
}

func (options boardroomOptions) validateListen() error {
	if err := options.validateCommon(); err != nil {
		return err
	}
	switch {
	case options.timeout < 0:
		return errors.New("timeout must be non-negative")
	case options.pollInterval < 0:
		return errors.New("poll-interval must be non-negative")
	default:
		return nil
	}
}

func (options boardroomOptions) validateCommon() error {
	switch {
	case options.serverURL == "":
		return errors.New("server is required")
	case options.sessionID == "":
		return errors.New("session-id is required")
	case options.leaseDuration <= 0:
		return errors.New("lease-duration must be positive")
	case !options.insecure && options.workerToken == "":
		return errors.New("worker-token is required (or pass --insecure for a serve --auth-disabled server)")
	default:
		return nil
	}
}

func boardroomRandomToken(random io.Reader) (string, error) {
	token := make([]byte, boardroomRandomBytes)
	if _, err := io.ReadFull(random, token); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}

func boardroomTTLSeconds(ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}
	seconds := int(ttl / time.Second)
	if ttl%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
