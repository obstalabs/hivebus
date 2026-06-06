package cli

import (
	"bytes"
	"crypto/ed25519"
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

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/spf13/cobra"
)

const (
	defaultAskFrom         = "hivebus/asker"
	defaultAskQuestionType = "direct"
	askMaxQuestionBytes    = 4096 // WO-84: keep query payloads bounded and read-only.
	askRandomTokenBytes    = 16   // WO-84: compact opaque envelope correlation tokens.
	askAnswerDelay         = time.Second
	defaultAskLiveTimeout  = 5 * time.Second
	defaultAskPollInterval = 100 * time.Millisecond
)

var (
	askNow                      = func() time.Time { return time.Now().UTC() }
	askRandomReader             = io.Reader(cryptoRand.Reader)
	askHTTPClient   askHTTPDoer = http.DefaultClient
	askSleep                    = time.Sleep

	askDeferredFollowups = []string{"broadcast-query", "offband-query", "answer-caching"}
)

type askOptions struct {
	from            string
	to              string
	questionType    string
	serverURL       string
	offline         bool
	timeout         time.Duration
	pollInterval    time.Duration
	sessionID       string // WO-98: runtime session ID used for live inbox polling.
	operatorToken   string
	workerToken     string // WO-98: RoleWorker bearer token for live ask inbox reads.
	answerPublicKey string
}

// WO-84: askExchange is the local fixture transport for the first ask->answer slice.
type askExchange struct {
	Query             model.Envelope `json:"query"`
	Answer            model.Envelope `json:"answer"`
	QueryPublicKey    string         `json:"query_public_key"`
	AnswerPublicKey   string         `json:"answer_public_key"`
	DeferredFollowups []string       `json:"deferred_followups"`
}

// WO-84: query payload has no requested_action or executable field by construction.
type askQueryPayload struct {
	Question     string `json:"question"`
	QuestionType string `json:"question_type"`
	ReadOnly     bool   `json:"read_only"`
}

// WO-84: answer payload is bounded metadata from the warm-context fixture receiver.
type askAnswerPayload struct {
	Answer       string `json:"answer"`
	AnsweredBy   string `json:"answered_by"`
	QuestionType string `json:"question_type"`
	ReadOnly     bool   `json:"read_only"`
}

type askHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type askLiveTransport struct {
	baseURL       string
	operatorToken string
	workerToken   string // WO-98: separate worker auth for runtime inbox polling.
	client        askHTTPDoer
	now           func() time.Time
	sleep         func(time.Duration)
}

// WO-97: signed query envelope travels through the runtime send message contract.
type askSendAgentMessageRequest struct {
	MessageID           string `json:"message_id"`
	SenderSessionID     string `json:"sender_session_id"`
	SenderParticipantID string `json:"sender_participant_id"`
	TargetParticipantID string `json:"target_participant_id"`
	Body                string `json:"body"`
}

type askSendAgentMessageResponse struct {
	Status  string          `json:"status"`
	Message json.RawMessage `json:"message"` // WO-99: runtime returns an object-valued AgentMessage.
	Receipt json.RawMessage `json:"receipt,omitempty"`
}

func newAskCommand() *cobra.Command {
	options := askOptions{
		from:         defaultAskFrom,
		questionType: defaultAskQuestionType,
		timeout:      defaultAskLiveTimeout,
		pollInterval: defaultAskPollInterval,
	}

	cmd := &cobra.Command{
		Use:   "ask --to <agent>",
		Short: "Ask a warm agent a read-only signed question",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			normalizedOptions := options
			normalizedOptions.normalize()
			if err := normalizedOptions.validate(); err != nil {
				return err
			}

			question, err := readAskQuestion(cmd.InOrStdin())
			if err != nil {
				return err
			}

			if normalizedOptions.useLiveDelivery() {
				exchange, err := buildLiveAskExchange(
					normalizedOptions,
					question,
					askNow(),
					askRandomReader,
					askLiveTransport{
						baseURL:       normalizedOptions.serverURL,
						operatorToken: normalizedOptions.operatorToken,
						workerToken:   normalizedOptions.workerToken,
						client:        askHTTPClient,
						now:           askNow,
						sleep:         askSleep,
					},
				)
				if err != nil {
					return err
				}

				return writeAskJSON(cmd.OutOrStdout(), exchange)
			}

			exchange, err := buildAskExchange(normalizedOptions, question, askNow(), askRandomReader)
			if err != nil {
				return err
			}
			return writeAskJSON(cmd.OutOrStdout(), exchange)
		},
	}

	cmd.Flags().StringVar(&options.to, "to", "", "target agent identity")                                      // WO-84: target warm agent.
	cmd.Flags().StringVar(&options.from, "from", defaultAskFrom, "asking agent identity")                      // WO-84: attributable asker.
	cmd.Flags().StringVar(&options.questionType, "type", defaultAskQuestionType, "question type label")        // WO-84: bounded query intent label.
	cmd.Flags().StringVar(&options.serverURL, "server", "", "hivebus server URL for live delivery")            // WO-94: opt into server-backed ask.
	cmd.Flags().BoolVar(&options.offline, "offline", false, "use the in-process fixture answer")               // WO-94: preserve fixture path.
	cmd.Flags().DurationVar(&options.timeout, "timeout", defaultAskLiveTimeout, "live answer timeout")         // WO-94: bounded live wait.
	cmd.Flags().DurationVar(&options.pollInterval, "poll-interval", defaultAskPollInterval, "answer poll gap") // WO-94: deterministic poll cadence.
	cmd.Flags().StringVar(&options.operatorToken, "operator-token", "", "operator auth token for live ask")    // WO-94: RoleOperator bearer token.
	cmd.Flags().StringVar(&options.sessionID, "session-id", "", "asking agent runtime session ID")             // WO-98: poll a real runtime session.
	cmd.Flags().StringVar(&options.workerToken, "worker-token", "", "worker auth token for live ask inbox")    // WO-98: RoleWorker bearer token.
	cmd.Flags().StringVar(
		&options.answerPublicKey,
		"answer-public-key",
		"",
		"base64 ed25519 public key expected to sign the answer",
	) // WO-94: verify answer provenance before trusting delivery.

	return cmd
}

func readAskQuestion(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, askMaxQuestionBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > askMaxQuestionBytes {
		return "", fmt.Errorf("question must be at most %d bytes", askMaxQuestionBytes)
	}

	question := strings.TrimSpace(string(data))
	if question == "" {
		return "", errors.New("question is required on stdin")
	}

	return question, nil
}

func buildAskExchange(options askOptions, question string, sentAt time.Time, random io.Reader) (askExchange, error) {
	options.normalize()
	if err := options.validate(); err != nil {
		return askExchange{}, err
	}
	if sentAt.IsZero() {
		return askExchange{}, errors.New("sent_at is required")
	}

	queryPublicKey, queryPrivateKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return askExchange{}, err
	}
	answerPublicKey, answerPrivateKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return askExchange{}, err
	}

	query, err := buildSignedAskQuery(options, question, sentAt, queryPrivateKey, random)
	if err != nil {
		return askExchange{}, err
	}

	answer, err := buildFixtureAnswer(query, queryPublicKey, answerPrivateKey, options.to, sentAt.Add(askAnswerDelay), random)
	if err != nil {
		return askExchange{}, err
	}

	return askExchange{
		Query:             query,
		Answer:            answer,
		QueryPublicKey:    base64.StdEncoding.EncodeToString(queryPublicKey),
		AnswerPublicKey:   base64.StdEncoding.EncodeToString(answerPublicKey),
		DeferredFollowups: append([]string(nil), askDeferredFollowups...),
	}, nil
}

func buildLiveAskExchange(
	options askOptions,
	question string,
	sentAt time.Time,
	random io.Reader,
	transport askLiveTransport,
) (askExchange, error) {
	options.normalize()
	if err := options.validate(); err != nil {
		return askExchange{}, err
	}
	if !options.useLiveDelivery() {
		return askExchange{}, errors.New("server is required for live ask")
	}
	if sentAt.IsZero() {
		return askExchange{}, errors.New("sent_at is required")
	}

	answerPublicKey, err := parseAskPublicKey(options.answerPublicKey)
	if err != nil {
		return askExchange{}, err
	}

	queryPublicKey, queryPrivateKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return askExchange{}, err
	}

	query, err := buildSignedAskQuery(options, question, sentAt, queryPrivateKey, random)
	if err != nil {
		return askExchange{}, err
	}
	queryPublicKeyEncoded := base64.StdEncoding.EncodeToString(queryPublicKey)

	if err := transport.sendQuery(query, options); err != nil {
		return askExchange{}, err
	}

	answer, err := transport.awaitAnswer(query, answerPublicKey, options)
	if err != nil {
		return askExchange{}, err
	}

	return askExchange{
		Query:             query,
		Answer:            answer,
		QueryPublicKey:    queryPublicKeyEncoded,
		AnswerPublicKey:   base64.StdEncoding.EncodeToString(answerPublicKey),
		DeferredFollowups: append([]string(nil), askDeferredFollowups...),
	}, nil
}

func buildSignedAskQuery(
	options askOptions,
	question string,
	sentAt time.Time,
	privateKey ed25519.PrivateKey,
	random io.Reader,
) (model.Envelope, error) {
	payload, err := json.Marshal(askQueryPayload{
		Question:     question,
		QuestionType: options.questionType,
		ReadOnly:     true,
	})
	if err != nil {
		return model.Envelope{}, err
	}

	messageToken, err := askRandomToken(random)
	if err != nil {
		return model.Envelope{}, err
	}
	threadToken, err := askRandomToken(random)
	if err != nil {
		return model.Envelope{}, err
	}
	nonceToken, err := askRandomToken(random)
	if err != nil {
		return model.Envelope{}, err
	}

	messageID := "query-" + messageToken
	envelope := model.Envelope{
		MessageID:      messageID,
		ThreadID:       "thread-" + threadToken,
		From:           options.from,
		To:             []string{options.to},
		Type:           model.MessageTypeQuery,
		Payload:        payload,
		SentAt:         sentAt,
		IdempotencyKey: "idem-" + messageID,
		Trace: model.Trace{
			CorrelationID:   "corr-" + threadToken,
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed,
		},
		Security: model.Security{
			Scheme: model.SecuritySchemeEd25519,
			Nonce:  "nonce-" + nonceToken,
		},
	}

	if err := validateAskQueryReadOnly(envelope); err != nil {
		return model.Envelope{}, err
	}

	return model.SignEnvelope(envelope, privateKey)
}

func buildFixtureAnswer(
	query model.Envelope,
	queryPublicKey ed25519.PublicKey,
	answerPrivateKey ed25519.PrivateKey,
	responder string,
	sentAt time.Time,
	random io.Reader,
) (model.Envelope, error) {
	if err := model.VerifyEnvelope(query, queryPublicKey); err != nil {
		return model.Envelope{}, fmt.Errorf("query signature verification failed: %w", err)
	}
	if err := validateAskQueryReadOnly(query); err != nil {
		return model.Envelope{}, err
	}

	queryPayload, err := decodeAskQueryPayload(query.Payload)
	if err != nil {
		return model.Envelope{}, err
	}
	answerPayload, err := json.Marshal(askAnswerPayload{
		Answer:       answerFromWarmContext(queryPayload.Question, responder, queryPayload.QuestionType),
		AnsweredBy:   responder,
		QuestionType: queryPayload.QuestionType,
		ReadOnly:     true,
	})
	if err != nil {
		return model.Envelope{}, err
	}

	messageToken, err := askRandomToken(random)
	if err != nil {
		return model.Envelope{}, err
	}
	nonceToken, err := askRandomToken(random)
	if err != nil {
		return model.Envelope{}, err
	}

	messageID := "answer-" + messageToken
	answer := model.Envelope{
		MessageID:      messageID,
		ThreadID:       query.ThreadID,
		From:           responder,
		To:             []string{query.From},
		Type:           model.MessageTypeAnswer,
		Payload:        answerPayload,
		ReplyTo:        query.MessageID,
		SentAt:         sentAt,
		IdempotencyKey: "idem-" + messageID,
		Trace: model.Trace{
			CorrelationID:   query.Trace.CorrelationID,
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed,
		},
		Security: model.Security{
			Scheme: model.SecuritySchemeEd25519,
			Nonce:  "nonce-" + nonceToken,
		},
	}

	return model.SignEnvelope(answer, answerPrivateKey)
}

func (transport askLiveTransport) sendQuery(
	query model.Envelope,
	options askOptions,
) error {
	body, err := json.Marshal(query)
	if err != nil {
		return err
	}

	requestPayload, err := json.Marshal(askSendAgentMessageRequest{
		MessageID:           query.MessageID,
		SenderSessionID:     options.sessionID,
		SenderParticipantID: options.from,
		TargetParticipantID: options.to,
		Body:                string(body),
	})
	if err != nil {
		return err
	}

	endpoint, err := askServerEndpoint(transport.baseURL, "/v0/agents/messages/send")
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(requestPayload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(transport.operatorToken) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(transport.operatorToken))
	}

	response, err := transport.httpClient().Do(request)
	if err != nil {
		return fmt.Errorf("send live ask query: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("send live ask query failed: status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var sendResponse askSendAgentMessageResponse
	if len(strings.TrimSpace(string(responseBody))) > 0 {
		if err := json.Unmarshal(responseBody, &sendResponse); err != nil {
			return fmt.Errorf("send live ask response must be json: %w", err)
		}
	}
	if sendResponse.Status != "" && !strings.EqualFold(sendResponse.Status, "ok") &&
		!strings.EqualFold(sendResponse.Status, "accepted") &&
		!strings.EqualFold(sendResponse.Status, "queued") {
		message := strings.TrimSpace(string(sendResponse.Message))
		var textMessage string
		if err := json.Unmarshal(sendResponse.Message, &textMessage); err == nil {
			message = strings.TrimSpace(textMessage)
		}
		if message == "" {
			message = sendResponse.Status
		}
		return fmt.Errorf("send live ask query failed: %s", message)
	}

	return nil
}

func (transport askLiveTransport) awaitAnswer(
	query model.Envelope,
	answerPublicKey ed25519.PublicKey,
	options askOptions,
) (model.Envelope, error) {
	now := transport.clock()
	deadline := now().Add(options.timeout)
	maxPolls := askMaxLivePolls(options.timeout, options.pollInterval)

	for attempt := 0; ; attempt++ {
		answers, err := transport.fetchInboxAnswers(options.sessionID)
		if err != nil {
			return model.Envelope{}, err
		}
		for _, answer := range answers {
			if answer.ReplyTo != query.MessageID || answer.ThreadID != query.ThreadID {
				continue
			}
			if err := validateLiveAskAnswer(query, answer, answerPublicKey, now()); err != nil {
				return model.Envelope{}, err
			}

			return answer, nil
		}

		if !now().Before(deadline) || attempt+1 >= maxPolls {
			return model.Envelope{}, fmt.Errorf(
				"no live answerer produced an answer before %s for query %s",
				options.timeout,
				query.MessageID,
			)
		}
		if options.pollInterval <= 0 {
			return model.Envelope{}, fmt.Errorf(
				"no live answerer produced an answer before %s for query %s",
				options.timeout,
				query.MessageID,
			)
		}
		transport.sleeper()(options.pollInterval)
	}
}

func askMaxLivePolls(timeout time.Duration, pollInterval time.Duration) int {
	if timeout <= 0 || pollInterval <= 0 {
		return 1
	}

	polls := int(timeout / pollInterval)
	if timeout%pollInterval != 0 {
		polls++
	}

	return polls + 1
}

func (transport askLiveTransport) fetchInboxAnswers(sessionID string) ([]model.Envelope, error) {
	endpoint, err := askServerEndpoint(
		transport.baseURL,
		"/v0/agents/sessions/"+url.PathEscape(sessionID)+"/inbox",
	)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(transport.workerToken) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(transport.workerToken))
	}

	response, err := transport.httpClient().Do(request)
	if err != nil {
		return nil, fmt.Errorf("poll live ask inbox: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("poll live ask inbox failed: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	answers, err := decodeAskInboxAnswers(body)
	if err != nil {
		return nil, err
	}

	return answers, nil
}

func validateLiveAskAnswer(
	query model.Envelope,
	answer model.Envelope,
	answerPublicKey ed25519.PublicKey,
	now time.Time,
) error {
	if err := model.VerifyEnvelope(answer, answerPublicKey); err != nil {
		return fmt.Errorf("answer signature verification failed: %w", err)
	}
	if answer.Type != model.MessageTypeAnswer {
		return fmt.Errorf("answer type = %q, want %q", answer.Type, model.MessageTypeAnswer)
	}
	if answer.ReplyTo != query.MessageID {
		return fmt.Errorf("answer reply_to = %q, want %q", answer.ReplyTo, query.MessageID)
	}
	if answer.ThreadID != query.ThreadID {
		return fmt.Errorf("answer thread_id = %q, want %q", answer.ThreadID, query.ThreadID)
	}
	// WO-101: bind signature-verified answers to this targeted ask route.
	if len(query.To) != 1 {
		return fmt.Errorf("query target count = %d, want 1", len(query.To))
	}
	if answer.From != query.To[0] {
		return fmt.Errorf("answer from = %q, want %q", answer.From, query.To[0])
	}
	if len(answer.To) != 1 {
		return fmt.Errorf("answer to count = %d, want 1 recipient %q", len(answer.To), query.From)
	}
	if answer.To[0] != query.From {
		return fmt.Errorf("answer to = %q, want %q", answer.To[0], query.From)
	}
	if answer.Deadline != nil && now.After(*answer.Deadline) {
		return errors.New("answer is expired")
	}

	return nil
}

func validateAskQueryReadOnly(envelope model.Envelope) error {
	if envelope.Type != model.MessageTypeQuery {
		return fmt.Errorf("ask query must use type %q", model.MessageTypeQuery)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Payload, &raw); err != nil {
		return fmt.Errorf("query payload must be json: %w", err)
	}
	if _, exists := raw["requested_action"]; exists {
		return errors.New("query payload must not include requested_action")
	}
	if _, exists := raw["executable"]; exists {
		return errors.New("query payload must not include executable")
	}

	query, err := decodeAskQueryPayload(envelope.Payload)
	if err != nil {
		return err
	}
	if !query.ReadOnly {
		return errors.New("query payload must be read_only")
	}

	return nil
}

func decodeAskInboxAnswers(data []byte) ([]model.Envelope, error) {
	return decodeAskInboxAnswersDepth(data, 0, true)
}

// WO-100: keep the top-level inbox contract strict but tolerate stale nested bodies.
func decodeAskInboxAnswersDepth(data []byte, depth int, strict bool) ([]model.Envelope, error) {
	if depth > 4 {
		if !strict {
			return nil, nil
		}
		return nil, errors.New("live ask inbox nesting is too deep")
	}

	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}

	var envelope model.Envelope
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.Type != "" {
		return []model.Envelope{envelope}, nil
	}

	var envelopeList []model.Envelope
	if err := json.Unmarshal(data, &envelopeList); err == nil {
		envelopes := make([]model.Envelope, 0, len(envelopeList))
		for _, listEnvelope := range envelopeList {
			if listEnvelope.Type != "" {
				envelopes = append(envelopes, listEnvelope)
			}
		}
		if len(envelopes) > 0 {
			return envelopes, nil
		}
	}

	var rawList []json.RawMessage
	if err := json.Unmarshal(data, &rawList); err == nil {
		var answers []model.Envelope
		for _, raw := range rawList {
			nested, err := decodeAskInboxAnswersDepth(raw, depth+1, false)
			if err != nil {
				// WO-100: stale queue entries may not be ask envelopes; keep scanning.
				continue
			}
			answers = append(answers, nested...)
		}

		return answers, nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		if !strict {
			return nil, nil
		}
		return nil, fmt.Errorf("live ask inbox must be json: %w", err)
	}

	var answers []model.Envelope
	for _, key := range []string{"messages", "items", "inbox", "answers"} {
		raw, exists := object[key]
		if !exists {
			continue
		}
		nested, err := decodeAskInboxAnswersDepth(raw, depth+1, false)
		if err != nil {
			if strict {
				return nil, err
			}
			continue
		}
		answers = append(answers, nested...)
	}

	for _, key := range []string{"body", "Body"} {
		raw, exists := object[key]
		if !exists {
			continue
		}
		var body string
		if err := json.Unmarshal(raw, &body); err != nil {
			if strict {
				return nil, fmt.Errorf("live ask inbox body must be a string: %w", err)
			}
			continue
		}
		nested, err := decodeAskInboxAnswersDepth([]byte(body), depth+1, false)
		if err != nil {
			if strict {
				return nil, err
			}
			continue
		}
		answers = append(answers, nested...)
	}

	for _, key := range []string{"envelope", "message"} {
		raw, exists := object[key]
		if !exists {
			continue
		}
		nested, err := decodeAskInboxAnswersDepth(raw, depth+1, false)
		if err != nil {
			if strict {
				return nil, err
			}
			continue
		}
		answers = append(answers, nested...)
	}

	return answers, nil
}

func decodeAskQueryPayload(payload json.RawMessage) (askQueryPayload, error) {
	var query askQueryPayload
	if err := json.Unmarshal(payload, &query); err != nil {
		return askQueryPayload{}, fmt.Errorf("query payload must be json: %w", err)
	}
	if strings.TrimSpace(query.Question) == "" {
		return askQueryPayload{}, errors.New("query.question is required")
	}
	if strings.TrimSpace(query.QuestionType) == "" {
		return askQueryPayload{}, errors.New("query.question_type is required")
	}

	return query, nil
}

func parseAskPublicKey(encoded string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("answer public key must be base64 ed25519: %w", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("answer public key size = %d, want %d", len(decoded), ed25519.PublicKeySize)
	}

	return ed25519.PublicKey(decoded), nil
}

func askServerEndpoint(rawBaseURL string, endpointPath string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil {
		return "", fmt.Errorf("server URL is invalid: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("server URL must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("server URL must include a host")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + endpointPath
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return parsed.String(), nil
}

func answerFromWarmContext(question string, responder string, questionType string) string {
	if strings.Contains(strings.ToLower(question), "which workledger checkout is canonical") {
		return fmt.Sprintf(
			"canonical checkout answer from %s (%s): warm context responds directly; no repository scan required",
			responder,
			questionType,
		)
	}

	return fmt.Sprintf("answer from %s (%s): %s", responder, questionType, question)
}

func askRandomToken(random io.Reader) (string, error) {
	token := make([]byte, askRandomTokenBytes)
	if _, err := io.ReadFull(random, token); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(token), nil
}

func writeAskJSON(writer io.Writer, exchange askExchange) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(exchange)
}

func (options *askOptions) normalize() {
	options.from = strings.TrimSpace(options.from)
	options.to = strings.TrimSpace(options.to)
	options.questionType = strings.TrimSpace(options.questionType)
	options.serverURL = strings.TrimRight(strings.TrimSpace(options.serverURL), "/")
	options.sessionID = strings.TrimSpace(options.sessionID)
	options.operatorToken = strings.TrimSpace(options.operatorToken)
	options.workerToken = strings.TrimSpace(options.workerToken)
	options.answerPublicKey = strings.TrimSpace(options.answerPublicKey)
}

func (options askOptions) validate() error {
	switch {
	case options.from == "":
		return errors.New("from is required")
	case options.to == "":
		return errors.New("to is required")
	case options.questionType == "":
		return errors.New("type is required")
	case options.offline && options.serverURL != "":
		return errors.New("offline cannot be combined with server")
	case options.timeout < 0:
		return errors.New("timeout must be non-negative")
	case options.pollInterval < 0:
		return errors.New("poll-interval must be non-negative")
	case options.useLiveDelivery() && options.sessionID == "":
		return errors.New("session-id is required for live ask inbox polling")
	case options.useLiveDelivery() && options.operatorToken == "":
		return errors.New("operator-token is required for live ask send")
	case options.useLiveDelivery() && options.workerToken == "":
		return errors.New("worker-token is required for live ask inbox polling")
	case options.useLiveDelivery() && options.answerPublicKey == "":
		return errors.New("answer-public-key is required for live ask verification")
	default:
		return nil
	}
}

func (options askOptions) useLiveDelivery() bool {
	return options.serverURL != "" && !options.offline
}

func (transport askLiveTransport) httpClient() askHTTPDoer {
	if transport.client != nil {
		return transport.client
	}

	return http.DefaultClient
}

func (transport askLiveTransport) clock() func() time.Time {
	if transport.now != nil {
		return transport.now
	}

	return time.Now
}

func (transport askLiveTransport) sleeper() func(time.Duration) {
	if transport.sleep != nil {
		return transport.sleep
	}

	return time.Sleep
}
