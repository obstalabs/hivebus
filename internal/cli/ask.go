package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	cryptoRand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
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
	askSelfRegisterLease   = 24 * time.Hour // WO-108: local dogfood asker sessions should survive daily loops.
	knownAnswerersDirMode  = 0o700          // WO-122: pin metadata is local trust state, not public discovery material.
	knownAnswerersFileMode = 0o600          // WO-122: match known_hosts-style private trust files.

	askResponseStatusAnswered   = "answered"   // WO-109: a verified response is present.
	askResponseStatusNoAnswer   = "no_answer"  // WO-109: delivery succeeded but no neuron answered.
	askResponseStatusUnverified = "unverified" // WO-109: delivery succeeded but response provenance failed.
)

var (
	askNow                      = func() time.Time { return time.Now().UTC() }
	askRandomReader             = io.Reader(cryptoRand.Reader)
	askHTTPClient   askHTTPDoer = http.DefaultClient
	askSleep                    = time.Sleep

	askDeferredFollowups = []string{"broadcast-query", "offband-query", "answer-caching"}
)

type askOptions struct {
	from               string
	to                 string
	questionType       string
	repoPath           string
	serverURL          string
	offline            bool
	timeout            time.Duration
	pollInterval       time.Duration
	sessionID          string // WO-98: runtime session ID used for live inbox polling.
	operatorToken      string
	workerToken        string // WO-98: RoleWorker bearer token for live ask inbox reads.
	answerPublicKey    string
	answerKeyFile      string // WO-108: local dogfood reads the answerer key without manual copy.
	knownAnswerersFile string // WO-122: tests pin outside the operator's known_answerers file.
	insecure           bool   // WO-104: allow tokenless live ask against a serve --auth-disabled server.
}

// WO-84: askExchange is the local fixture transport for the first ask->answer slice.
// WO-109: live asks also use it to separate delivery success from response success.
type askExchange struct {
	Delivered         bool           `json:"delivered"`                    // WO-109: delivery is hivebus state, separate from response success.
	Recipient         string         `json:"recipient"`                    // WO-109: report the addressed recipient set even when no answer is trusted.
	QueryMessageID    string         `json:"query_message_id"`             // WO-109: correlate delivered-no-answer reports without needing an answer.
	Answers           int            `json:"answers"`                      // WO-109: trusted response count, not delivery success.
	ResponseStatus    string         `json:"response_status"`              // WO-109: distinguish no answer from untrusted response provenance.
	VerificationError string         `json:"verification_error,omitempty"` // WO-109: untrusted responses are provenance failures, not delivery failures.
	Query             model.Envelope `json:"query"`
	Answer            model.Envelope `json:"answer"`
	QueryPublicKey    string         `json:"query_public_key"`
	AnswerPublicKey   string         `json:"answer_public_key"`
	DeferredFollowups []string       `json:"deferred_followups"`
}

// WO-84: query payload has no requested_action or executable field by construction.
type askQueryPayload struct {
	Question       string `json:"question"`
	QuestionType   string `json:"question_type"`
	ReadOnly       bool   `json:"read_only"`
	QueryPublicKey string `json:"query_public_key,omitempty"` // WO-95: answerers need the signed query's verification key in-band.
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
	trustLog      io.Writer
	registered    map[string]struct{} // WO-108: avoid duplicate self-registration inside one ask run.
}

// WO-109: awaiting an answer is response provenance; inbox transport errors still
// remain delivery/runtime errors, but no answer and untrusted answers do not.
type askAnswerWaitResult struct {
	Answer            model.Envelope
	Found             bool
	VerificationError string
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
		Long: strings.Join([]string{
			"Ask a warm agent a read-only signed question.",
			"",
			"Live ask exit semantics:",
			"  delivery failed: non-zero error; the query did not reach the addressed recipient set",
			"  delivered answered: exit 0 with delivered=true and answers=1",
			"  delivered no-answer or unverified answer: exit 0 with delivered=true and answers=0",
		}, "\n"),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			normalizedOptions := options
			normalizedOptions.normalize()
			if err := normalizedOptions.loadAnswerKeyFile(); err != nil {
				return err
			}
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
						trustLog:      cmd.ErrOrStderr(),
						registered:    make(map[string]struct{}),
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
	cmd.Flags().StringVar(&options.repoPath, "repo", "", "addressed repository path for repo_status answers")  // WO-119: bind repo_status verification to an addressed worktree.
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
	cmd.Flags().StringVar(
		&options.answerKeyFile,
		"answer-public-key-file",
		"",
		"path containing the base64 ed25519 public key expected to sign the answer",
	) // WO-108: local dogfood can consume the answerer's key file without hand-copying.
	cmd.Flags().StringVar(
		&options.knownAnswerersFile,
		"known-answerers-file",
		"",
		"path to the known_answerers pin file",
	) // WO-122: testable override for local answerer trust pins.
	cmd.Flags().BoolVar(&options.insecure, "insecure", false, "allow tokenless live ask against a serve --auth-disabled server") // WO-104: match serve --auth-disabled on the dogfood path.

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
		Delivered:         true,
		Recipient:         options.to,
		QueryMessageID:    query.MessageID,
		Answers:           1,
		ResponseStatus:    askResponseStatusAnswered,
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

	var answerPublicKey ed25519.PublicKey
	var answerPublicKeyEncoded string
	if options.answerPublicKey != "" {
		var err error
		answerPublicKey, err = parseAskPublicKey(options.answerPublicKey)
		if err != nil {
			return askExchange{}, err
		}
		answerPublicKeyEncoded = base64.StdEncoding.EncodeToString(answerPublicKey)
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

	// WO-108: only the explicit insecure dogfood path self-provisions an asker session.
	if err := transport.registerAskerSession(options); err != nil {
		return askExchange{}, err
	}
	sendResponse, err := transport.sendQuery(query, options)
	if err != nil {
		return askExchange{}, err
	}
	if answerPublicKey == nil {
		resolvedKey, err := answerPublicKeyFromSendResponse(sendResponse, options.to)
		if err != nil {
			return askExchange{}, err
		}
		answerPublicKey, err = pinResolvedAnswerKey(
			options.to,
			resolvedKey,
			options.knownAnswerersFile,
			transport.clock()(),
			transport.trustLog,
		)
		if err != nil {
			return askExchange{}, err
		}
		answerPublicKeyEncoded = base64.StdEncoding.EncodeToString(answerPublicKey)
	}

	answerResult, err := transport.awaitAnswer(query, answerPublicKey, options)
	if err != nil {
		return askExchange{}, err
	}

	exchange := askExchange{
		Delivered:         true,
		Recipient:         options.to,
		QueryMessageID:    query.MessageID,
		Answers:           0,
		ResponseStatus:    askResponseStatusNoAnswer,
		Query:             query,
		Answer:            model.Envelope{},
		QueryPublicKey:    queryPublicKeyEncoded,
		AnswerPublicKey:   answerPublicKeyEncoded,
		DeferredFollowups: append([]string(nil), askDeferredFollowups...),
	}
	if answerResult.VerificationError != "" {
		exchange.ResponseStatus = askResponseStatusUnverified
		exchange.VerificationError = answerResult.VerificationError
	}
	if answerResult.Found {
		exchange.Answer = answerResult.Answer
		exchange.Answers = 1
		exchange.ResponseStatus = askResponseStatusAnswered
		exchange.VerificationError = ""
	}

	return exchange, nil
}

func buildSignedAskQuery(
	options askOptions,
	question string,
	sentAt time.Time,
	privateKey ed25519.PrivateKey,
	random io.Reader,
) (model.Envelope, error) {
	payload, err := json.Marshal(askQueryPayload{
		Question:       question,
		QuestionType:   options.questionType,
		ReadOnly:       true,
		QueryPublicKey: base64.StdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
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

func (transport askLiveTransport) registerAskerSession(options askOptions) error {
	if !options.insecure {
		return nil
	}
	if transport.registered != nil {
		if _, exists := transport.registered[options.sessionID]; exists {
			return nil
		}
	}

	payload := model.AgentSessionPayload{
		AgentID:        options.sessionID,
		InstallationID: "local",
		SessionID:      options.sessionID,
		ParticipantID:  options.from,
		DeliveryMode:   model.AgentDeliveryQueued,
		SessionStatus:  model.AgentSessionOnline,
		LeaseExpiresAt: transport.clock()().Add(askSelfRegisterLease).UTC().Format(time.RFC3339),
	}
	requestPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint, err := askServerEndpoint(transport.baseURL, "/v0/agents/sessions/register")
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(requestPayload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(transport.workerToken) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(transport.workerToken))
	}

	response, err := transport.httpClient().Do(request)
	if err != nil {
		return fmt.Errorf("register live ask session: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf(
			"register live ask session failed: status %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(responseBody)),
		)
	}
	if transport.registered != nil {
		transport.registered[options.sessionID] = struct{}{}
	}

	return nil
}

func (transport askLiveTransport) sendQuery(
	query model.Envelope,
	options askOptions,
) (askSendAgentMessageResponse, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return askSendAgentMessageResponse{}, err
	}

	requestPayload, err := json.Marshal(askSendAgentMessageRequest{
		MessageID:           query.MessageID,
		SenderSessionID:     options.sessionID,
		SenderParticipantID: options.from,
		TargetParticipantID: options.to,
		Body:                string(body),
	})
	if err != nil {
		return askSendAgentMessageResponse{}, err
	}

	endpoint, err := askServerEndpoint(transport.baseURL, "/v0/agents/messages/send")
	if err != nil {
		return askSendAgentMessageResponse{}, err
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(requestPayload))
	if err != nil {
		return askSendAgentMessageResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(transport.operatorToken) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(transport.operatorToken))
	}

	response, err := transport.httpClient().Do(request)
	if err != nil {
		return askSendAgentMessageResponse{}, fmt.Errorf("send live ask query: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return askSendAgentMessageResponse{}, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return askSendAgentMessageResponse{}, fmt.Errorf("send live ask query failed: status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var sendResponse askSendAgentMessageResponse
	if len(strings.TrimSpace(string(responseBody))) > 0 {
		if err := json.Unmarshal(responseBody, &sendResponse); err != nil {
			return askSendAgentMessageResponse{}, fmt.Errorf("send live ask response must be json: %w", err)
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
		return askSendAgentMessageResponse{}, fmt.Errorf("send live ask query failed: %s", message)
	}

	return sendResponse, nil
}

func (transport askLiveTransport) awaitAnswer(
	query model.Envelope,
	answerPublicKey ed25519.PublicKey,
	options askOptions,
) (askAnswerWaitResult, error) {
	now := transport.clock()
	deadline := now().Add(options.timeout)
	maxPolls := askMaxLivePolls(options.timeout, options.pollInterval)
	// WO-110: keep scanning for a valid answer after the first bad candidate.
	var verificationError string

	for attempt := 0; ; attempt++ {
		answers, err := transport.fetchInboxAnswers(options.sessionID)
		if err != nil {
			return askAnswerWaitResult{}, err
		}
		for _, answer := range answers {
			if !isLiveAskAnswerCandidate(query, answer) {
				continue
			}
			if err := validateLiveAskAnswerForRepo(query, answer, answerPublicKey, now(), options.repoPath); err != nil {
				if verificationError == "" {
					verificationError = err.Error()
				}
				continue
			}

			return askAnswerWaitResult{Answer: answer, Found: true}, nil
		}

		if !now().Before(deadline) || attempt+1 >= maxPolls {
			if verificationError != "" {
				return askAnswerWaitResult{VerificationError: verificationError}, nil
			}
			return askAnswerWaitResult{}, nil
		}
		if options.pollInterval <= 0 {
			if verificationError != "" {
				return askAnswerWaitResult{VerificationError: verificationError}, nil
			}
			return askAnswerWaitResult{}, nil
		}
		transport.sleeper()(options.pollInterval)
	}
}

func isLiveAskAnswerCandidate(query model.Envelope, answer model.Envelope) bool {
	// WO-109: a partial correlation match is a response provenance error; an
	// unrelated inbox message should not poison this delivered query.
	return answer.ReplyTo == query.MessageID || answer.ThreadID == query.ThreadID
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
	return validateLiveAskAnswerForRepo(query, answer, answerPublicKey, now, "")
}

func validateLiveAskAnswerForRepo(
	query model.Envelope,
	answer model.Envelope,
	answerPublicKey ed25519.PublicKey,
	now time.Time,
	addressedRepoPath string,
) error {
	if answer.Type != model.MessageTypeAnswer {
		return fmt.Errorf("answer type = %q, want %q", answer.Type, model.MessageTypeAnswer)
	}
	if answer.ReplyTo != query.MessageID {
		return fmt.Errorf("answer reply_to = %q, want %q", answer.ReplyTo, query.MessageID)
	}
	if answer.ThreadID != query.ThreadID {
		return fmt.Errorf("answer thread_id = %q, want %q", answer.ThreadID, query.ThreadID)
	}
	if err := validateLiveAskAnswerRoute(query, answer); err != nil {
		return err
	}
	if err := model.VerifyEnvelope(answer, answerPublicKey); err != nil {
		return fmt.Errorf("answer signature verification failed: %w", err)
	}
	if answer.Deadline != nil && now.After(*answer.Deadline) {
		return errors.New("answer is expired")
	}
	if err := validateLiveAskRepoStatusObservation(query, answer, now, addressedRepoPath); err != nil {
		return err
	}

	return nil
}

func validateLiveAskRepoStatusObservation(
	query model.Envelope,
	answer model.Envelope,
	now time.Time,
	addressedRepoPath string,
) error {
	queryPayload, err := decodeAskQueryPayload(query.Payload)
	if err != nil {
		return err
	}
	if !isRepoStatusQuestionType(queryPayload.QuestionType) {
		return nil
	}

	if strings.TrimSpace(addressedRepoPath) == "" {
		return errors.New("repo_status addressed repo is required")
	}
	addressedRepoID, err := canonicalRepoPath(addressedRepoPath)
	if err != nil {
		return fmt.Errorf("resolve addressed repo: %w", err)
	}

	var payload repoStatusAnswerPayload
	if err := json.Unmarshal(answer.Payload, &payload); err != nil {
		return fmt.Errorf("repo_status payload must be json: %w", err)
	}
	if payload.TrustClass != answerTrustClassToolAsserted {
		return fmt.Errorf("repo_status trust_class = %q, want %q", payload.TrustClass, answerTrustClassToolAsserted)
	}
	if !payload.ReadOnly {
		return errors.New("repo_status payload must be read_only")
	}
	if !isRepoStatusQuestionType(payload.QuestionType) {
		return fmt.Errorf("repo_status question_type = %q, want repo_status", payload.QuestionType)
	}
	if strings.TrimSpace(payload.RepoID) == "" {
		return errors.New("repo_status repo_id is required")
	}
	if payload.RepoID != addressedRepoID {
		return fmt.Errorf("answer observed wrong repo: %s != %s", payload.RepoID, addressedRepoID)
	}
	if strings.TrimSpace(payload.GitHeadSHA) == "" {
		return errors.New("repo_status git_head_sha is required")
	}
	if strings.TrimSpace(payload.Head.Full) != "" && payload.GitHeadSHA != payload.Head.Full {
		return fmt.Errorf("repo_status git_head_sha = %q, want head.full %q", payload.GitHeadSHA, payload.Head.Full)
	}
	if strings.TrimSpace(payload.AbsoluteGitDir) == "" {
		return errors.New("repo_status absolute_git_dir is required")
	}
	if payload.ObservedAt.IsZero() {
		return errors.New("repo_status observed_at is required")
	}
	if payload.ExpiresAt.IsZero() {
		return errors.New("repo_status expires_at is required")
	}
	if now.After(payload.ExpiresAt) {
		return errors.New("repo_status observation is expired")
	}

	expectedGitDir, expectedDev, expectedIno, resolved := addressedRepoGitIdentity(context.Background(), addressedRepoID)
	if !resolved {
		return nil
	}
	if canonicalPathForCompare(payload.AbsoluteGitDir) != canonicalPathForCompare(expectedGitDir) {
		return fmt.Errorf("answer observed wrong git dir: %s != %s", payload.AbsoluteGitDir, expectedGitDir)
	}
	if payload.GitDirDev == 0 || payload.GitDirIno == 0 || expectedDev == 0 || expectedIno == 0 {
		return nil
	}
	if payload.GitDirDev != expectedDev || payload.GitDirIno != expectedIno {
		return fmt.Errorf(
			"answer observed wrong git dir inode: %d:%d != %d:%d",
			payload.GitDirDev,
			payload.GitDirIno,
			expectedDev,
			expectedIno,
		)
	}

	return nil
}

func addressedRepoGitIdentity(ctx context.Context, repoPath string) (string, uint64, uint64, bool) {
	absoluteGitDir, err := runGitCommand(ctx, repoPath, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", 0, 0, false
	}
	absoluteGitDir = strings.TrimSpace(absoluteGitDir)
	if absoluteGitDir == "" {
		return "", 0, 0, false
	}
	dev, ino := gitDirDeviceInode(absoluteGitDir)

	return absoluteGitDir, dev, ino, true
}

func canonicalPathForCompare(path string) string {
	canonical, err := canonicalRepoPath(path)
	if err != nil {
		return strings.TrimSpace(path)
	}

	return canonical
}

func validateLiveAskAnswerRoute(query model.Envelope, answer model.Envelope) error {
	target, err := liveAskQueryTarget(query)
	if err != nil {
		return err
	}
	// WO-101: bind signed answers to this targeted ask route.
	if answer.From != target {
		return fmt.Errorf("answer from = %q, want %q", answer.From, target)
	}
	// WO-102: canonical targeted routing may replace the legacy answer.to mirror.
	if answer.Scope == model.ScopeTargeted {
		if answer.Recipient == "" {
			return errors.New("answer recipient is required for targeted route")
		}
		if answer.Recipient != query.From {
			return fmt.Errorf("answer recipient = %q, want %q", answer.Recipient, query.From)
		}
		if len(answer.To) > 1 {
			return fmt.Errorf("answer to count = %d, want 0 or 1 mirror recipient %q", len(answer.To), answer.Recipient)
		}
		if len(answer.To) == 1 && answer.To[0] != answer.Recipient {
			return fmt.Errorf("answer to = %q, want recipient %q", answer.To[0], answer.Recipient)
		}

		return nil
	}
	if answer.Scope != "" {
		return fmt.Errorf("answer scope = %q, want %q", answer.Scope, model.ScopeTargeted)
	}
	// WO-101: preserve the legacy single-To route contract.
	if len(answer.To) != 1 {
		return fmt.Errorf("answer to count = %d, want 1 recipient %q", len(answer.To), query.From)
	}
	if answer.To[0] != query.From {
		return fmt.Errorf("answer to = %q, want %q", answer.To[0], query.From)
	}
	if answer.Recipient != "" && answer.Recipient != query.From {
		return fmt.Errorf("answer recipient = %q, want %q", answer.Recipient, query.From)
	}

	return nil
}

func liveAskQueryTarget(query model.Envelope) (string, error) {
	// WO-102: prefer canonical targeted query recipient when present.
	if query.Scope == model.ScopeTargeted {
		if query.Recipient == "" {
			return "", errors.New("query recipient is required for targeted route")
		}
		if len(query.To) > 1 {
			return "", fmt.Errorf("query to count = %d, want 0 or 1 mirror recipient %q", len(query.To), query.Recipient)
		}
		if len(query.To) == 1 && query.To[0] != query.Recipient {
			return "", fmt.Errorf("query to = %q, want recipient %q", query.To[0], query.Recipient)
		}

		return query.Recipient, nil
	}
	if query.Scope != "" {
		return "", fmt.Errorf("query scope = %q, want %q", query.Scope, model.ScopeTargeted)
	}
	// WO-101: legacy live ask is still a single targeted recipient.
	if len(query.To) != 1 {
		return "", fmt.Errorf("query target count = %d, want 1", len(query.To))
	}
	if query.Recipient != "" && query.Recipient != query.To[0] {
		return "", fmt.Errorf("query recipient = %q, want %q", query.Recipient, query.To[0])
	}

	return query.To[0], nil
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

func answerPublicKeyFromSendResponse(response askSendAgentMessageResponse, agentID string) (string, error) {
	var message struct {
		TargetAnswerPublicKey string `json:"target_answer_public_key"`
	}
	if len(response.Message) > 0 {
		if err := json.Unmarshal(response.Message, &message); err != nil {
			return "", fmt.Errorf("decode live ask send response message: %w", err)
		}
	}
	resolvedKey := strings.TrimSpace(message.TargetAnswerPublicKey)
	if resolvedKey == "" {
		return "", fmt.Errorf("answer public key for agent %s is not available from bus session record", agentID)
	}

	return resolvedKey, nil
}

func pinResolvedAnswerKey(
	agentID string,
	encodedKey string,
	knownAnswerersFile string,
	now time.Time,
	log io.Writer,
) (ed25519.PublicKey, error) {
	publicKey, err := parseAskPublicKey(encodedKey)
	if err != nil {
		return nil, err
	}
	path, err := knownAnswerersPath(knownAnswerersFile)
	if err != nil {
		return nil, err
	}

	pinnedKey, found, err := loadKnownAnswererPin(path, agentID)
	if err != nil {
		return nil, err
	}
	if found {
		pinnedPublicKey, err := parseAskPublicKey(pinnedKey)
		if err != nil {
			return nil, fmt.Errorf("known answerer pin for agent %s is invalid: %w", agentID, err)
		}
		if bytes.Equal(pinnedPublicKey, publicKey) {
			return publicKey, nil
		}
		return nil, fmt.Errorf(
			"answer key mismatch for agent %s: pinned %s, bus offers %s -- possible impersonation; if the answerer legitimately rotated keys, remove the entry from %s",
			agentID,
			answerKeyFingerprint(pinnedPublicKey),
			answerKeyFingerprint(publicKey),
			path,
		)
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}
	canonicalKey := base64.StdEncoding.EncodeToString(publicKey)
	if err := appendKnownAnswererPin(path, agentID, canonicalKey, now.UTC()); err != nil {
		return nil, err
	}
	if log != nil {
		_, _ = fmt.Fprintf(log, "pinned agent_id=%s fingerprint=%s\n", agentID, answerKeyFingerprint(publicKey))
	}

	return publicKey, nil
}

func knownAnswerersPath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		return strings.TrimSpace(path), nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve known_answerers path: %w", err)
	}

	return filepath.Join(homeDir, ".hivebus", "known_answerers"), nil
}

func loadKnownAnswererPin(path string, agentID string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read known_answerers: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	for lineNumber, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return "", false, fmt.Errorf("known_answerers line %d must be: agent_id base64-key first-seen-rfc3339", lineNumber+1)
		}
		if _, err := time.Parse(time.RFC3339, fields[2]); err != nil {
			return "", false, fmt.Errorf("known_answerers line %d first-seen must be RFC3339: %w", lineNumber+1, err)
		}
		if fields[0] == agentID {
			return fields[1], true, nil
		}
	}

	return "", false, nil
}

func appendKnownAnswererPin(path string, agentID string, encodedKey string, firstSeen time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), knownAnswerersDirMode); err != nil {
		return fmt.Errorf("create known_answerers directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, knownAnswerersFileMode)
	if err != nil {
		return fmt.Errorf("open known_answerers: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	if err := file.Chmod(knownAnswerersFileMode); err != nil {
		return fmt.Errorf("chmod known_answerers: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%s %s %s\n", agentID, encodedKey, firstSeen.Format(time.RFC3339)); err != nil {
		return fmt.Errorf("write known_answerers: %w", err)
	}

	return nil
}

func answerKeyFingerprint(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}

func (options *askOptions) loadAnswerKeyFile() error {
	if options.answerKeyFile == "" {
		return nil
	}

	data, err := os.ReadFile(options.answerKeyFile)
	if err != nil {
		return fmt.Errorf("read answer-public-key-file: %w", err)
	}
	fileKey := strings.TrimSpace(string(data))
	if fileKey == "" {
		return errors.New("answer-public-key-file is empty")
	}
	if options.answerPublicKey != "" && options.answerPublicKey != fileKey {
		return errors.New("answer-public-key and answer-public-key-file differ")
	}
	options.answerPublicKey = fileKey

	return nil
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
	options.repoPath = strings.TrimSpace(options.repoPath)
	options.serverURL = strings.TrimRight(strings.TrimSpace(options.serverURL), "/")
	options.sessionID = strings.TrimSpace(options.sessionID)
	options.operatorToken = strings.TrimSpace(options.operatorToken)
	options.workerToken = strings.TrimSpace(options.workerToken)
	options.answerPublicKey = strings.TrimSpace(options.answerPublicKey)
	options.answerKeyFile = strings.TrimSpace(options.answerKeyFile)
	options.knownAnswerersFile = strings.TrimSpace(options.knownAnswerersFile)
}

func (options askOptions) validate() error {
	switch {
	case options.from == "":
		return errors.New("from is required")
	case options.to == "":
		return errors.New("to is required")
	case options.questionType == "":
		return errors.New("type is required")
	case isRepoStatusQuestionType(options.questionType) && options.repoPath == "":
		return errors.New("repo is required for repo_status ask verification")
	case options.offline && options.serverURL != "":
		return errors.New("offline cannot be combined with server")
	case options.timeout < 0:
		return errors.New("timeout must be non-negative")
	case options.pollInterval < 0:
		return errors.New("poll-interval must be non-negative")
	case options.useLiveDelivery() && options.sessionID == "":
		return errors.New("session-id is required for live ask inbox polling")
	case options.useLiveDelivery() && !options.insecure && options.operatorToken == "":
		return errors.New("operator-token is required for live ask send (or pass --insecure for a serve --auth-disabled server)")
	case options.useLiveDelivery() && !options.insecure && options.workerToken == "":
		return errors.New("worker-token is required for live ask inbox polling (or pass --insecure for a serve --auth-disabled server)")
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
