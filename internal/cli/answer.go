package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	cryptoRand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/spf13/cobra"
)

const (
	defaultAnswerPollInterval      = 500 * time.Millisecond
	defaultAnswerHeartbeatInterval = 15 * time.Second
	defaultAnswerLeaseDuration     = 2 * time.Minute
	defaultAnswerTTL               = 30 * time.Second
	answerMaxPayloadBytes          = 8192 // WO-95: repo cards must stay bounded and fresh.
	answerRandomTokenBytes         = 16
	answerPublicKeyFileMode        = 0o644 // WO-108: public keys are non-secret local discovery material.
	answerPublicKeyDirMode         = 0o755 // WO-108: create missing key-file parents for dogfood startup.
	answerTrustClassToolAsserted   = "tool_asserted"
	answerUnsupportedQueryClass    = "unsupported_query_class"
	answerRepoStatusUnavailable    = "repo_status_unavailable"
)

var (
	answerNow                      = func() time.Time { return time.Now().UTC() }
	answerRandomReader             = io.Reader(cryptoRand.Reader)
	answerHTTPClient   askHTTPDoer = http.DefaultClient
)

type answerOptions struct {
	agentID           string
	serverURL         string
	sessionID         string
	installationID    string
	project           string
	repoPath          string
	pollInterval      time.Duration
	heartbeatInterval time.Duration
	leaseDuration     time.Duration
	answerTTL         time.Duration
	operatorToken     string
	workerToken       string
	insecure          bool
	signingKey        string
	publicKeyFile     string // WO-108: publish the answer verification key for local ask.
	printPublicKey    bool   // WO-108: deterministic key discovery without entering the loop.
	leaseID           string
	workOrder         string
}

type answerStartupOutput struct {
	Status          string `json:"status"`
	AgentID         string `json:"agent_id"`
	SessionID       string `json:"session_id"`
	ParticipantID   string `json:"participant_id"`
	AnswerPublicKey string `json:"answer_public_key"`
}

type answerInboxResponse struct {
	Status   string                     `json:"status"`
	Messages []answerAgentMessageRecord `json:"messages"`
}

type answerAgentMessageRecord struct {
	Message answerAgentMessage `json:"message"`
}

type answerAgentMessage struct {
	MessageID           string `json:"message_id"`
	SenderSessionID     string `json:"sender_session_id"`
	SenderParticipantID string `json:"sender_participant_id"`
	TargetParticipantID string `json:"target_participant_id"`
	Body                string `json:"body"`
}

type answerSendAgentMessageRequest struct {
	MessageID           string `json:"message_id"`
	SenderSessionID     string `json:"sender_session_id"`
	SenderParticipantID string `json:"sender_participant_id"`
	TargetParticipantID string `json:"target_participant_id"`
	Body                string `json:"body"`
	TTLSeconds          int    `json:"ttl_seconds,omitempty"`
}

type answerStatusResponse struct {
	Status  string          `json:"status"`
	Message json.RawMessage `json:"message,omitempty"`
}

type answerTransport interface {
	RegisterSession(context.Context, model.AgentSessionPayload) error
	HeartbeatSession(context.Context, model.AgentSessionPayload) error
	FetchInbox(context.Context, string) ([]answerAgentMessageRecord, error)
	SendAnswer(context.Context, answerSendAgentMessageRequest) error
	DeliverMessage(context.Context, string, string) error
}

type repoStatusResolver interface {
	ResolveRepoStatus(context.Context, repoStatusRequest) (repoStatusFacts, error)
}

type repoStatusRequest struct {
	AgentID   string
	Project   string
	RepoPath  string
	LeaseID   string
	WorkOrder string
}

type repoStatusFacts struct {
	AgentID           string `json:"agent_id"`
	Project           string `json:"project"`
	CanonicalRepoPath string `json:"canonical_repo_path"`
	Remote            string `json:"remote"`
	Branch            string `json:"branch"`
	HeadShort         string `json:"head_short"`
	HeadFull          string `json:"head_full"`
	Dirty             bool   `json:"dirty"`
	WorktreeRole      string `json:"worktree_role"`
	LeaseID           string `json:"lease,omitempty"`
	WorkOrder         string `json:"wo,omitempty"`
}

type repoStatusAnswerPayload struct {
	QuestionType      string `json:"question_type"`
	ReadOnly          bool   `json:"read_only"`
	TrustClass        string `json:"trust_class"`
	AgentID           string `json:"agent_id"`
	Project           string `json:"project"`
	CanonicalRepoPath string `json:"canonical_repo_path"`
	Remote            string `json:"remote"`
	Branch            string `json:"branch"`
	Head              struct {
		Short string `json:"short"`
		Full  string `json:"full"`
	} `json:"head"`
	Dirty        bool      `json:"dirty"`
	WorktreeRole string    `json:"worktree_role"`
	LeaseID      string    `json:"lease,omitempty"`
	WorkOrder    string    `json:"wo,omitempty"`
	ObservedAt   time.Time `json:"observed_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Nonce        string    `json:"nonce"`
}

type unsupportedAnswerPayload struct {
	UnsupportedQueryClass string    `json:"unsupported_query_class"`
	QuestionType          string    `json:"question_type"`
	ReadOnly              bool      `json:"read_only"`
	AnsweredBy            string    `json:"answered_by"`
	ObservedAt            time.Time `json:"observed_at"`
	ExpiresAt             time.Time `json:"expires_at"`
	Nonce                 string    `json:"nonce"`
}

type answerRunner struct {
	options    answerOptions
	transport  answerTransport
	resolver   repoStatusResolver
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	random     io.Reader
	now        func() time.Time
	logs       io.Writer
	answered   map[string]struct{}
}

type answerHTTPTransport struct {
	baseURL       string
	operatorToken string
	workerToken   string
	client        askHTTPDoer
}

type gitRepoStatusResolver struct {
	runGit func(context.Context, string, ...string) (string, error)
}

func newAnswerCommand() *cobra.Command {
	options := answerOptions{
		pollInterval:      defaultAnswerPollInterval,
		heartbeatInterval: defaultAnswerHeartbeatInterval,
		leaseDuration:     defaultAnswerLeaseDuration,
		answerTTL:         defaultAnswerTTL,
		installationID:    "local",
	}

	cmd := &cobra.Command{
		Use:   "answer --agent <id> --server <url>",
		Short: "Run a conservative signed answerer loop",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAnswer(
				cmd.Context(),
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
				options,
				answerRuntimeDeps{
					client: answerHTTPClient,
					random: answerRandomReader,
					now:    answerNow,
				},
			)
		},
	}

	cmd.Flags().StringVar(&options.agentID, "agent", "", "answering agent identity")
	cmd.Flags().StringVar(&options.serverURL, "server", "", "hivebus server URL")
	cmd.Flags().StringVar(&options.sessionID, "session-id", "", "runtime session ID")
	cmd.Flags().StringVar(&options.installationID, "installation-id", "local", "installation identity")
	cmd.Flags().StringVar(&options.project, "project", "", "project label for repo_status answers")
	cmd.Flags().StringVar(&options.repoPath, "repo", "", "repository path for repo_status answers")
	cmd.Flags().DurationVar(&options.pollInterval, "poll-interval", defaultAnswerPollInterval, "inbox poll interval")
	cmd.Flags().DurationVar(&options.heartbeatInterval, "heartbeat-interval", defaultAnswerHeartbeatInterval, "heartbeat interval")
	cmd.Flags().DurationVar(&options.leaseDuration, "lease-duration", defaultAnswerLeaseDuration, "session lease duration")
	cmd.Flags().DurationVar(&options.answerTTL, "answer-ttl", defaultAnswerTTL, "signed answer freshness window")
	cmd.Flags().StringVar(&options.operatorToken, "operator-token", "", "operator auth token for answer send")
	cmd.Flags().StringVar(&options.workerToken, "worker-token", "", "worker auth token for register, heartbeat, inbox, and deliver")
	cmd.Flags().BoolVar(&options.insecure, "insecure", false, "allow tokenless answerer calls against a serve --auth-disabled server")
	cmd.Flags().StringVar(&options.signingKey, "signing-key", "", "base64 ed25519 private key or seed for answer signatures")
	cmd.Flags().StringVar(&options.publicKeyFile, "public-key-file", "", "path to write the base64 ed25519 answer public key")     // WO-108: file plumbing only, no registry.
	cmd.Flags().BoolVar(&options.printPublicKey, "print-public-key", false, "print the base64 ed25519 answer public key and exit") // WO-108: stdout-only discovery mode.
	cmd.Flags().StringVar(&options.leaseID, "lease", "", "optional lease provenance for repo_status answers")
	cmd.Flags().StringVar(&options.workOrder, "wo", "", "optional work-order provenance for repo_status answers")

	return cmd
}

type answerRuntimeDeps struct {
	client askHTTPDoer
	random io.Reader
	now    func() time.Time
}

func runAnswer(
	ctx context.Context,
	out io.Writer,
	logs io.Writer,
	options answerOptions,
	deps answerRuntimeDeps,
) error {
	options.normalize()
	if options.printPublicKey {
		publicKey, _, err := answerSigningKey(options.signingKey, deps.randomReader())
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, answerPublicKeyString(publicKey))
		return err
	}
	if options.repoPath == "" {
		repoPath, err := os.Getwd()
		if err != nil {
			return err
		}
		options.repoPath = repoPath
	}
	if options.project == "" {
		options.project = filepath.Base(options.repoPath)
	}
	if options.sessionID == "" {
		token, err := answerRandomToken(deps.randomReader())
		if err != nil {
			return err
		}
		options.sessionID = "answer-" + token
	}
	if err := options.validate(); err != nil {
		return err
	}

	publicKey, privateKey, err := answerSigningKey(options.signingKey, deps.randomReader())
	if err != nil {
		return err
	}

	runner := answerRunner{
		options: options,
		transport: answerHTTPTransport{
			baseURL:       options.serverURL,
			operatorToken: options.operatorToken,
			workerToken:   options.workerToken,
			client:        deps.httpClient(),
		},
		resolver:   gitRepoStatusResolver{},
		privateKey: privateKey,
		publicKey:  publicKey,
		random:     deps.randomReader(),
		now:        deps.clock(),
		logs:       logs,
		answered:   make(map[string]struct{}),
	}

	signalCtx, stop := signalNotifyContext(ctx)
	defer stop()

	return runner.run(signalCtx, out)
}

func (runner *answerRunner) run(ctx context.Context, out io.Writer) error {
	if runner.answered == nil {
		runner.answered = make(map[string]struct{})
	}
	if err := runner.register(ctx); err != nil {
		return err
	}
	encodedPublicKey := answerPublicKeyString(runner.publicKey)
	if err := writeAnswerPublicKeyFile(runner.options.publicKeyFile, encodedPublicKey); err != nil {
		return err
	}
	if err := writeJSON(out, answerStartupOutput{
		Status:          "registered",
		AgentID:         runner.options.agentID,
		SessionID:       runner.options.sessionID,
		ParticipantID:   runner.options.agentID,
		AnswerPublicKey: encodedPublicKey,
	}); err != nil {
		return err
	}
	runner.logf("answerer registered agent_id=%s session_id=%s", runner.options.agentID, runner.options.sessionID)

	if err := runner.pollOnce(ctx); err != nil {
		runner.logf("answerer poll error=%q", err)
	}

	pollTicker := time.NewTicker(runner.options.pollInterval)
	defer pollTicker.Stop()
	heartbeatTicker := time.NewTicker(runner.options.heartbeatInterval)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeatTicker.C:
			if err := runner.heartbeat(ctx); err != nil {
				runner.logf("answerer heartbeat error=%q", err)
			}
		case <-pollTicker.C:
			if err := runner.pollOnce(ctx); err != nil {
				runner.logf("answerer poll error=%q", err)
			}
		}
	}
}

func (runner *answerRunner) register(ctx context.Context) error {
	return runner.transport.RegisterSession(ctx, runner.sessionPayload())
}

func (runner *answerRunner) heartbeat(ctx context.Context) error {
	return runner.transport.HeartbeatSession(ctx, runner.sessionPayload())
}

func (runner *answerRunner) sessionPayload() model.AgentSessionPayload {
	leaseExpiresAt := runner.clock()().Add(runner.options.leaseDuration).UTC()
	return model.AgentSessionPayload{
		AgentID:        runner.options.agentID,
		InstallationID: runner.options.installationID,
		SessionID:      runner.options.sessionID,
		ParticipantID:  runner.options.agentID,
		Capabilities:   []string{"repo_status", "canonical_worktree_status"},
		Roles:          []string{"answerer"},
		DeliveryMode:   model.AgentDeliveryQueued,
		SessionStatus:  model.AgentSessionOnline,
		LeaseExpiresAt: leaseExpiresAt.Format(time.RFC3339),
	}
}

func (runner *answerRunner) pollOnce(ctx context.Context) error {
	records, err := runner.transport.FetchInbox(ctx, runner.options.sessionID)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := runner.handleMessage(ctx, record); err != nil {
			return err
		}
	}

	return nil
}

func (runner *answerRunner) handleMessage(ctx context.Context, record answerAgentMessageRecord) error {
	recordID := strings.TrimSpace(record.Message.MessageID)
	var query model.Envelope
	if err := json.Unmarshal([]byte(record.Message.Body), &query); err != nil {
		runner.logRefused(recordID, "", "malformed_query_body")
		return runner.deliverIfPossible(ctx, recordID)
	}
	if query.Type != model.MessageTypeQuery {
		runner.logRefused(recordID, query.MessageID, "non_query_message")
		return runner.deliverIfPossible(ctx, recordID)
	}
	if _, exists := runner.answered[query.MessageID]; exists {
		return runner.deliverIfPossible(ctx, recordID)
	}

	queryPayload, queryPublicKey, err := verifiedAnswerQuery(query)
	if err != nil {
		runner.logRefused(recordID, query.MessageID, "query_verification_failed")
		return runner.deliverIfPossible(ctx, recordID)
	}
	if err := model.VerifyEnvelope(query, queryPublicKey); err != nil {
		runner.logRefused(recordID, query.MessageID, "query_signature_verification_failed")
		return runner.deliverIfPossible(ctx, recordID)
	}
	if err := validateAskQueryReadOnly(query); err != nil {
		runner.logRefused(recordID, query.MessageID, "query_not_read_only")
		return runner.deliverIfPossible(ctx, recordID)
	}
	if err := runner.validateQueryRoute(query); err != nil {
		runner.logRefused(recordID, query.MessageID, "query_route_mismatch")
		return runner.deliverIfPossible(ctx, recordID)
	}

	answer, refused, err := runner.buildAnswer(ctx, query, queryPayload)
	if err != nil {
		return err
	}
	request, err := runner.answerSendRequest(answer, queryPayload.QuestionType)
	if err != nil {
		return err
	}

	// WO-95: /deliver only records receipt of the inbound queue item; the signed
	// answer must go through RoleOperator /send because that is the only handler
	// whose request body carries a message payload.
	if err := runner.transport.SendAnswer(ctx, request); err != nil {
		return err
	}
	runner.answered[query.MessageID] = struct{}{}

	if refused {
		runner.logRefused(recordID, query.MessageID, answerUnsupportedQueryClass)
	} else {
		runner.logf(
			"answered query_id=%s question_type=%s answer_id=%s",
			query.MessageID,
			queryPayload.QuestionType,
			answer.MessageID,
		)
	}

	return runner.deliverIfPossible(ctx, recordID)
}

func (runner *answerRunner) buildAnswer(
	ctx context.Context,
	query model.Envelope,
	queryPayload askQueryPayload,
) (model.Envelope, bool, error) {
	observedAt := runner.clock()()
	expiresAt := observedAt.Add(runner.options.answerTTL).UTC()
	nonce, err := answerRandomToken(runner.randomReader())
	if err != nil {
		return model.Envelope{}, false, err
	}

	var payload []byte
	refused := false
	switch {
	case isRepoStatusQuestionType(queryPayload.QuestionType):
		facts, err := runner.resolver.ResolveRepoStatus(ctx, repoStatusRequest{
			AgentID:   runner.options.agentID,
			Project:   runner.options.project,
			RepoPath:  runner.options.repoPath,
			LeaseID:   runner.options.leaseID,
			WorkOrder: runner.options.workOrder,
		})
		if err != nil {
			refused = true
			payload, err = marshalBoundedAnswerPayload(unsupportedAnswerPayload{
				UnsupportedQueryClass: answerRepoStatusUnavailable,
				QuestionType:          queryPayload.QuestionType,
				ReadOnly:              true,
				AnsweredBy:            runner.options.agentID,
				ObservedAt:            observedAt,
				ExpiresAt:             expiresAt,
				Nonce:                 nonce,
			})
			if err != nil {
				return model.Envelope{}, false, err
			}
			break
		}
		payload, err = marshalBoundedAnswerPayload(repoStatusPayload(queryPayload.QuestionType, facts, observedAt, expiresAt, nonce))
		if err != nil {
			return model.Envelope{}, false, err
		}
	default:
		refused = true
		payload, err = marshalBoundedAnswerPayload(unsupportedAnswerPayload{
			UnsupportedQueryClass: queryPayload.QuestionType,
			QuestionType:          queryPayload.QuestionType,
			ReadOnly:              true,
			AnsweredBy:            runner.options.agentID,
			ObservedAt:            observedAt,
			ExpiresAt:             expiresAt,
			Nonce:                 nonce,
		})
		if err != nil {
			return model.Envelope{}, false, err
		}
	}

	messageToken, err := answerRandomToken(runner.randomReader())
	if err != nil {
		return model.Envelope{}, false, err
	}
	signatureNonce, err := answerRandomToken(runner.randomReader())
	if err != nil {
		return model.Envelope{}, false, err
	}
	messageID := "answer-" + messageToken
	answer := model.Envelope{
		MessageID:      messageID,
		ThreadID:       query.ThreadID,
		From:           runner.options.agentID,
		To:             []string{query.From},
		Type:           model.MessageTypeAnswer,
		Payload:        payload,
		ReplyTo:        query.MessageID,
		Deadline:       &expiresAt,
		SentAt:         observedAt,
		IdempotencyKey: "idem-" + messageID,
		Trace: model.Trace{
			CorrelationID:   query.Trace.CorrelationID,
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed,
		},
		Security: model.Security{
			Scheme: model.SecuritySchemeEd25519,
			Nonce:  "nonce-" + signatureNonce,
		},
	}
	if query.Scope == model.ScopeTargeted {
		answer.Scope = model.ScopeTargeted
		answer.Recipient = query.From
	}

	signed, err := model.SignEnvelope(answer, runner.privateKey)
	if err != nil {
		return model.Envelope{}, false, err
	}

	return signed, refused, nil
}

func (runner *answerRunner) answerSendRequest(answer model.Envelope, questionType string) (answerSendAgentMessageRequest, error) {
	body, err := json.Marshal(answer)
	if err != nil {
		return answerSendAgentMessageRequest{}, err
	}
	target := answerTargetParticipant(answer)
	if target == "" {
		return answerSendAgentMessageRequest{}, errors.New("answer target participant is required")
	}

	return answerSendAgentMessageRequest{
		MessageID:           answer.MessageID,
		SenderSessionID:     runner.options.sessionID,
		SenderParticipantID: runner.options.agentID,
		TargetParticipantID: target,
		Body:                string(body),
		TTLSeconds:          answerTTLSeconds(runner.options.answerTTL),
	}, nil
}

func (runner *answerRunner) validateQueryRoute(query model.Envelope) error {
	target, err := liveAskQueryTarget(query)
	if err != nil {
		return err
	}
	if target != runner.options.agentID {
		return fmt.Errorf("query target = %q, want %q", target, runner.options.agentID)
	}

	return nil
}

func (runner *answerRunner) deliverIfPossible(ctx context.Context, messageID string) error {
	if strings.TrimSpace(messageID) == "" {
		return nil
	}

	return runner.transport.DeliverMessage(ctx, messageID, runner.options.sessionID)
}

func (runner *answerRunner) logRefused(recordID string, queryID string, reason string) {
	runner.logf("refused message_id=%s query_id=%s reason=%s", recordID, queryID, reason)
}

func (runner *answerRunner) logf(format string, args ...any) {
	if runner.logs == nil {
		return
	}
	_, _ = fmt.Fprintf(runner.logs, format+"\n", args...)
}

func repoStatusPayload(
	questionType string,
	facts repoStatusFacts,
	observedAt time.Time,
	expiresAt time.Time,
	nonce string,
) repoStatusAnswerPayload {
	payload := repoStatusAnswerPayload{
		QuestionType:      questionType,
		ReadOnly:          true,
		TrustClass:        answerTrustClassToolAsserted,
		AgentID:           facts.AgentID,
		Project:           facts.Project,
		CanonicalRepoPath: facts.CanonicalRepoPath,
		Remote:            facts.Remote,
		Branch:            facts.Branch,
		Dirty:             facts.Dirty,
		WorktreeRole:      facts.WorktreeRole,
		LeaseID:           facts.LeaseID,
		WorkOrder:         facts.WorkOrder,
		ObservedAt:        observedAt,
		ExpiresAt:         expiresAt,
		Nonce:             nonce,
	}
	payload.Head.Short = facts.HeadShort
	payload.Head.Full = facts.HeadFull

	return payload
}

func marshalBoundedAnswerPayload(payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(data) > answerMaxPayloadBytes {
		return nil, fmt.Errorf("answer payload is %d bytes, max %d", len(data), answerMaxPayloadBytes)
	}

	return data, nil
}

func verifiedAnswerQuery(query model.Envelope) (askQueryPayload, ed25519.PublicKey, error) {
	queryPayload, err := decodeAskQueryPayload(query.Payload)
	if err != nil {
		return askQueryPayload{}, nil, err
	}
	publicKey, err := parseAnswerQueryPublicKey(queryPayload.QueryPublicKey)
	if err != nil {
		return askQueryPayload{}, nil, err
	}

	return queryPayload, publicKey, nil
}

func parseAnswerQueryPublicKey(encoded string) (ed25519.PublicKey, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, errors.New("query_public_key is required")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("query_public_key must be base64 ed25519: %w", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("query_public_key size = %d, want %d", len(decoded), ed25519.PublicKeySize)
	}

	return ed25519.PublicKey(decoded), nil
}

func isRepoStatusQuestionType(questionType string) bool {
	switch strings.TrimSpace(questionType) {
	case "repo_status", "canonical_worktree_status":
		return true
	default:
		return false
	}
}

func answerTargetParticipant(answer model.Envelope) string {
	if answer.Scope == model.ScopeTargeted && answer.Recipient != "" {
		return answer.Recipient
	}
	if len(answer.To) == 1 {
		return answer.To[0]
	}

	return ""
}

func answerTTLSeconds(ttl time.Duration) int {
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

func answerSigningKey(encoded string, random io.Reader) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		publicKey, privateKey, err := ed25519.GenerateKey(random)
		return publicKey, privateKey, err
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, nil, fmt.Errorf("signing-key must be base64 ed25519 private key or seed: %w", err)
	}
	switch len(decoded) {
	case ed25519.SeedSize:
		privateKey := ed25519.NewKeyFromSeed(decoded)
		return privateKey.Public().(ed25519.PublicKey), privateKey, nil
	case ed25519.PrivateKeySize:
		privateKey := ed25519.PrivateKey(decoded)
		return privateKey.Public().(ed25519.PublicKey), privateKey, nil
	default:
		return nil, nil, fmt.Errorf(
			"signing-key size = %d, want %d byte seed or %d byte private key",
			len(decoded),
			ed25519.SeedSize,
			ed25519.PrivateKeySize,
		)
	}
}

func answerPublicKeyString(publicKey ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(publicKey)
}

func writeAnswerPublicKeyFile(path string, encodedPublicKey string) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, answerPublicKeyDirMode); err != nil {
			return fmt.Errorf("create public-key-file directory: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(encodedPublicKey+"\n"), answerPublicKeyFileMode); err != nil {
		return fmt.Errorf("write public-key-file: %w", err)
	}

	return nil
}

func answerRandomToken(random io.Reader) (string, error) {
	token := make([]byte, answerRandomTokenBytes)
	if _, err := io.ReadFull(random, token); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(token), nil
}

func (options *answerOptions) normalize() {
	options.agentID = strings.TrimSpace(options.agentID)
	options.serverURL = strings.TrimRight(strings.TrimSpace(options.serverURL), "/")
	options.sessionID = strings.TrimSpace(options.sessionID)
	options.installationID = strings.TrimSpace(options.installationID)
	options.project = strings.TrimSpace(options.project)
	options.repoPath = strings.TrimSpace(options.repoPath)
	options.operatorToken = strings.TrimSpace(options.operatorToken)
	options.workerToken = strings.TrimSpace(options.workerToken)
	options.signingKey = strings.TrimSpace(options.signingKey)
	options.publicKeyFile = strings.TrimSpace(options.publicKeyFile)
	options.leaseID = strings.TrimSpace(options.leaseID)
	options.workOrder = strings.TrimSpace(options.workOrder)
}

func (options answerOptions) validate() error {
	switch {
	case options.agentID == "":
		return errors.New("agent is required")
	case options.serverURL == "":
		return errors.New("server is required")
	case options.sessionID == "":
		return errors.New("session-id is required")
	case options.installationID == "":
		return errors.New("installation-id is required")
	case options.project == "":
		return errors.New("project is required")
	case options.repoPath == "":
		return errors.New("repo is required")
	case options.pollInterval <= 0:
		return errors.New("poll-interval must be positive")
	case options.heartbeatInterval <= 0:
		return errors.New("heartbeat-interval must be positive")
	case options.leaseDuration <= 0:
		return errors.New("lease-duration must be positive")
	case options.answerTTL <= 0:
		return errors.New("answer-ttl must be positive")
	case !options.insecure && options.operatorToken == "":
		return errors.New("operator-token is required for answer send (or pass --insecure for a serve --auth-disabled server)")
	case !options.insecure && options.workerToken == "":
		return errors.New("worker-token is required for answer register/inbox/deliver (or pass --insecure for a serve --auth-disabled server)")
	default:
		return nil
	}
}

func (deps answerRuntimeDeps) httpClient() askHTTPDoer {
	if deps.client != nil {
		return deps.client
	}

	return http.DefaultClient
}

func (deps answerRuntimeDeps) randomReader() io.Reader {
	if deps.random != nil {
		return deps.random
	}

	return cryptoRand.Reader
}

func (deps answerRuntimeDeps) clock() func() time.Time {
	if deps.now != nil {
		return deps.now
	}

	return time.Now
}

func (runner answerRunner) clock() func() time.Time {
	if runner.now != nil {
		return runner.now
	}

	return time.Now
}

func (runner answerRunner) randomReader() io.Reader {
	if runner.random != nil {
		return runner.random
	}

	return cryptoRand.Reader
}

func (transport answerHTTPTransport) RegisterSession(ctx context.Context, payload model.AgentSessionPayload) error {
	return transport.postJSON(ctx, "/v0/agents/sessions/register", transport.workerToken, payload)
}

func (transport answerHTTPTransport) HeartbeatSession(ctx context.Context, payload model.AgentSessionPayload) error {
	return transport.postJSON(ctx, "/v0/agents/sessions/heartbeat", transport.workerToken, payload)
}

func (transport answerHTTPTransport) FetchInbox(ctx context.Context, sessionID string) ([]answerAgentMessageRecord, error) {
	endpointPath := "/v0/agents/sessions/" + url.PathEscape(sessionID) + "/inbox"
	var responsePayload answerInboxResponse
	if err := transport.doJSON(ctx, http.MethodGet, endpointPath, transport.workerToken, nil, &responsePayload); err != nil {
		return nil, err
	}

	return responsePayload.Messages, nil
}

func (transport answerHTTPTransport) SendAnswer(ctx context.Context, request answerSendAgentMessageRequest) error {
	return transport.postJSON(ctx, "/v0/agents/messages/send", transport.operatorToken, request)
}

func (transport answerHTTPTransport) DeliverMessage(ctx context.Context, messageID string, sessionID string) error {
	endpointPath := "/v0/agents/messages/" + url.PathEscape(messageID) + "/deliver"
	return transport.postJSON(ctx, endpointPath, transport.workerToken, map[string]string{"session_id": sessionID})
}

func (transport answerHTTPTransport) postJSON(ctx context.Context, path string, token string, payload any) error {
	return transport.doJSON(ctx, http.MethodPost, path, token, payload, nil)
}

func (transport answerHTTPTransport) doJSON(
	ctx context.Context,
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

	endpoint, err := askServerEndpoint(transport.baseURL, path)
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

	response, err := transport.httpClient().Do(request)
	if err != nil {
		return fmt.Errorf("answerer %s %s: %w", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("answerer %s %s failed: status %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if responsePayload != nil && len(strings.TrimSpace(string(responseBody))) > 0 {
		if err := json.Unmarshal(responseBody, responsePayload); err != nil {
			return fmt.Errorf("answerer %s %s response must be json: %w", method, path, err)
		}
	}
	if err := validateAnswerStatus(responseBody); err != nil {
		return fmt.Errorf("answerer %s %s failed: %w", method, path, err)
	}

	return nil
}

func (transport answerHTTPTransport) httpClient() askHTTPDoer {
	if transport.client != nil {
		return transport.client
	}

	return http.DefaultClient
}

func validateAnswerStatus(responseBody []byte) error {
	if len(strings.TrimSpace(string(responseBody))) == 0 {
		return nil
	}
	var status answerStatusResponse
	if err := json.Unmarshal(responseBody, &status); err != nil {
		return nil
	}
	if status.Status == "" {
		return nil
	}
	switch strings.ToLower(status.Status) {
	case "ok", "accepted", "queued", "registered", "heartbeat", "delivered":
		return nil
	default:
		message := strings.TrimSpace(string(status.Message))
		var textMessage string
		if err := json.Unmarshal(status.Message, &textMessage); err == nil {
			message = strings.TrimSpace(textMessage)
		}
		if message == "" {
			message = status.Status
		}
		return errors.New(message)
	}
}

func (resolver gitRepoStatusResolver) ResolveRepoStatus(ctx context.Context, request repoStatusRequest) (repoStatusFacts, error) {
	repoPath, err := canonicalRepoPath(request.RepoPath)
	if err != nil {
		return repoStatusFacts{}, err
	}
	runGit := resolver.runGit
	if runGit == nil {
		runGit = runGitCommand
	}

	fullHead, err := runGit(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		return repoStatusFacts{}, err
	}
	shortHead, err := runGit(ctx, repoPath, "rev-parse", "--short", "HEAD")
	if err != nil {
		return repoStatusFacts{}, err
	}
	branch, err := runGit(ctx, repoPath, "branch", "--show-current")
	if err != nil {
		return repoStatusFacts{}, err
	}
	if strings.TrimSpace(branch) == "" {
		branch = "HEAD"
	}
	remote, err := runGit(ctx, repoPath, "config", "--get", "remote.origin.url")
	if err != nil {
		remote = ""
	}
	status, err := runGit(ctx, repoPath, "status", "--porcelain")
	if err != nil {
		return repoStatusFacts{}, err
	}
	worktreeRole, err := gitWorktreeRole(ctx, repoPath, runGit)
	if err != nil {
		return repoStatusFacts{}, err
	}

	return repoStatusFacts{
		AgentID:           request.AgentID,
		Project:           request.Project,
		CanonicalRepoPath: repoPath,
		Remote:            strings.TrimSpace(remote),
		Branch:            strings.TrimSpace(branch),
		HeadShort:         strings.TrimSpace(shortHead),
		HeadFull:          strings.TrimSpace(fullHead),
		Dirty:             strings.TrimSpace(status) != "",
		WorktreeRole:      worktreeRole,
		LeaseID:           request.LeaseID,
		WorkOrder:         request.WorkOrder,
	}, nil
}

func canonicalRepoPath(repoPath string) (string, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(repoPath))
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		return resolved, nil
	}

	return absPath, nil
}

func gitWorktreeRole(
	ctx context.Context,
	repoPath string,
	runGit func(context.Context, string, ...string) (string, error),
) (string, error) {
	insideWorktree, err := runGit(ctx, repoPath, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(insideWorktree) != "true" {
		return "bare", nil
	}
	commonDir, err := runGit(ctx, repoPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	commonDir = strings.TrimSpace(commonDir)
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repoPath, commonDir)
	}
	commonDir, err = canonicalRepoPath(commonDir)
	if err != nil {
		return "", err
	}
	defaultDir, err := canonicalRepoPath(filepath.Join(repoPath, ".git"))
	if err != nil {
		return "", err
	}
	if commonDir == defaultDir {
		return "primary_worktree", nil
	}

	return "linked_worktree", nil
}

func runGitCommand(ctx context.Context, repoPath string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", repoPath}, args...)...)
	output, err := command.Output()
	if err != nil {
		return "", err
	}

	return string(output), nil
}
