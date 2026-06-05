package cli

import (
	"crypto/ed25519"
	cryptoRand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
)

var (
	askNow          = func() time.Time { return time.Now().UTC() }
	askRandomReader = io.Reader(cryptoRand.Reader)

	askDeferredFollowups = []string{"broadcast-query", "offband-query", "answer-caching"}
)

type askOptions struct {
	from         string
	to           string
	questionType string
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

func newAskCommand() *cobra.Command {
	options := askOptions{
		from:         defaultAskFrom,
		questionType: defaultAskQuestionType,
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

			exchange, err := buildAskExchange(normalizedOptions, question, askNow(), askRandomReader)
			if err != nil {
				return err
			}

			return writeAskJSON(cmd.OutOrStdout(), exchange)
		},
	}

	cmd.Flags().StringVar(&options.to, "to", "", "target agent identity")                               // WO-84: target warm agent.
	cmd.Flags().StringVar(&options.from, "from", defaultAskFrom, "asking agent identity")               // WO-84: attributable asker.
	cmd.Flags().StringVar(&options.questionType, "type", defaultAskQuestionType, "question type label") // WO-84: bounded query intent label.

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
}

func (options askOptions) validate() error {
	switch {
	case options.from == "":
		return errors.New("from is required")
	case options.to == "":
		return errors.New("to is required")
	case options.questionType == "":
		return errors.New("type is required")
	default:
		return nil
	}
}
