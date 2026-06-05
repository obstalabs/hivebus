package cli

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/artifact"
	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/runtime"
	"github.com/ppiankov/hivebus/internal/store"
)

// WO-96: prove `hivebus ask --server` round-trips through the ACTUAL runtime
// handler contract, not a hand-shaped fake. The prior live tests stand up
// httptest.HandlerFunc stubs the test author wrote to match the CLI, so they
// cannot catch a CLI-vs-real-server divergence. This test mounts the real
// runtime.NewHandler router with a real store and drives ask end-to-end:
// the asker sends a signed query through /v0/agents/messages/send, an answerer
// goroutine reads the real answerer inbox and replies through the same real
// send path, and ask polls its real asker inbox and verifies the answer.
func TestAskCommandRoundTripsThroughRealRuntimeHandler(t *testing.T) {
	withDeterministicAskRuntime(t)

	const (
		askerSession    = "sess-asker-runtime"
		askerAgent      = "architect/agent"
		answererSession = "sess-answerer-runtime"
		answererAgent   = "workledger/agent"
		operatorToken   = "operator-secret"
		workerToken     = "worker-secret"
	)

	handler := newRuntimeAskHandler(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	// Both participants must be registered/online: QueueAgentMessage routes by
	// resolving the target participant to an online session, so an unregistered
	// target would be dropped — a real-contract fact a fake handler hides.
	registerRuntimeAskSession(t, server.URL, workerToken, askerSession, askerAgent)
	registerRuntimeAskSession(t, server.URL, workerToken, answererSession, answererAgent)

	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(11)

	// The answerer plays a real warm agent: poll the answerer inbox over the
	// real handler, and when the signed query arrives, send a signed answer
	// back to the asker through the same real /send path.
	answerer := startRuntimeAnswerer(t, server.URL, runtimeAnswererConfig{
		operatorToken:   operatorToken,
		workerToken:     workerToken,
		answererSession: answererSession,
		answererAgent:   answererAgent,
		answerPrivate:   answerPrivateKey,
	})
	defer answerer.stop()

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", server.URL,
		"--to", answererAgent,
		"--from", askerAgent,
		"--type", "canonical_repo",
		"--session-id", askerSession,
		"--operator-token", operatorToken,
		"--worker-token", workerToken,
		"--answer-public-key", base64.StdEncoding.EncodeToString(answerPublicKey),
		"--timeout", "2s",
		"--poll-interval", "20ms",
	})
	cmd.SetIn(strings.NewReader("which workledger checkout is canonical and what's HEAD?\n"))

	var output bytes.Buffer
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("ask command against real runtime error = %v", err)
	}

	var exchange askExchange
	if err := json.Unmarshal(output.Bytes(), &exchange); err != nil {
		t.Fatalf("Unmarshal(exchange) error = %v", err)
	}
	if exchange.Answer.Type != model.MessageTypeAnswer {
		t.Fatalf("answer type = %q, want %q", exchange.Answer.Type, model.MessageTypeAnswer)
	}
	if exchange.Answer.ReplyTo != exchange.Query.MessageID {
		t.Fatalf("answer reply_to = %q, want query %q", exchange.Answer.ReplyTo, exchange.Query.MessageID)
	}
	if exchange.Answer.ThreadID != exchange.Query.ThreadID {
		t.Fatalf("answer thread_id = %q, want query %q", exchange.Answer.ThreadID, exchange.Query.ThreadID)
	}
	if exchange.Answer.From != answererAgent {
		t.Fatalf("answer from = %q, want %q", exchange.Answer.From, answererAgent)
	}
	if err := model.VerifyEnvelope(exchange.Answer, answerPublicKey); err != nil {
		t.Fatalf("VerifyEnvelope(answer) error = %v", err)
	}
}

func newRuntimeAskHandler(t *testing.T) http.Handler {
	t.Helper()

	st, err := store.Open(t.TempDir() + "/hivebus.db")
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	artifacts, err := artifact.Open(t.TempDir())
	if err != nil {
		t.Fatalf("artifact.Open() error = %v", err)
	}

	entries := []runtime.TokenEntry{
		{ID: "worker", KeyHash: runtime.HashToken("worker-secret"), Role: runtime.RoleWorker},
		{ID: "operator", KeyHash: runtime.HashToken("operator-secret"), Role: runtime.RoleOperator},
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("Marshal(token entries) error = %v", err)
	}
	keys, err := runtime.ParseKeyStore(data)
	if err != nil {
		t.Fatalf("ParseKeyStore() error = %v", err)
	}

	return runtime.NewHandler(st, artifacts, keys)
}

func registerRuntimeAskSession(t *testing.T, baseURL, workerToken, sessionID, participantID string) {
	t.Helper()

	payload := model.AgentSessionPayload{
		AgentID:        sessionID,
		InstallationID: "install-" + sessionID,
		SessionID:      sessionID,
		ParticipantID:  participantID,
		DeliveryMode:   model.AgentDeliveryQueued,
		SessionStatus:  model.AgentSessionOnline,
		LeaseExpiresAt: time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal(register payload) error = %v", err)
	}

	request, err := http.NewRequest(http.MethodPost, baseURL+"/v0/agents/sessions/register", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest(register) error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+workerToken)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("register %s error = %v", sessionID, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("register %s status = %d", sessionID, response.StatusCode)
	}
}

type runtimeAnswererConfig struct {
	operatorToken   string
	workerToken     string
	answererSession string
	answererAgent   string
	answerPrivate   ed25519.PrivateKey
}

type runtimeAnswerer struct {
	done chan struct{}
	stop func()
}

// startRuntimeAnswerer runs a minimal real warm agent: poll the answerer inbox
// over the real handler, and for each signed query reply with one signed answer
// sent back to the asker through the real /send path.
func startRuntimeAnswerer(t *testing.T, baseURL string, cfg runtimeAnswererConfig) *runtimeAnswerer {
	t.Helper()

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	var once sync.Once

	go func() {
		defer close(doneCh)
		answered := make(map[string]struct{})
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				messages := pollRuntimeInbox(t, baseURL, cfg.workerToken, cfg.answererSession)
				for _, record := range messages {
					queryID := record.Message.MessageID
					if _, seen := answered[queryID]; seen {
						continue
					}
					var query model.Envelope
					if err := json.Unmarshal([]byte(record.Message.Body), &query); err != nil {
						continue
					}
					if query.Type != model.MessageTypeQuery {
						continue
					}
					answered[queryID] = struct{}{}
					sendRuntimeAnswer(t, baseURL, cfg, query)
				}
			}
		}
	}()

	return &runtimeAnswerer{
		done: doneCh,
		stop: func() {
			once.Do(func() { close(stopCh) })
			<-doneCh
		},
	}
}

func pollRuntimeInbox(t *testing.T, baseURL, workerToken, sessionID string) []store.AgentMessageRecord {
	t.Helper()

	request, err := http.NewRequest(http.MethodGet, baseURL+"/v0/agents/sessions/"+sessionID+"/inbox", nil)
	if err != nil {
		t.Fatalf("NewRequest(inbox) error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+workerToken)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil
	}

	var inbox struct {
		Messages []store.AgentMessageRecord `json:"messages"`
	}
	if err := json.NewDecoder(response.Body).Decode(&inbox); err != nil {
		return nil
	}
	return inbox.Messages
}

func sendRuntimeAnswer(t *testing.T, baseURL string, cfg runtimeAnswererConfig, query model.Envelope) {
	t.Helper()

	answer := model.Envelope{
		MessageID:      "answer-" + query.MessageID,
		ThreadID:       query.ThreadID,
		From:           cfg.answererAgent,
		To:             []string{query.From},
		Type:           model.MessageTypeAnswer,
		Payload:        json.RawMessage(`{"answer":"canonical checkout: obstalabs-github/hivebus","answered_by":"workledger/agent","question_type":"canonical_repo","read_only":true}`),
		ReplyTo:        query.MessageID,
		SentAt:         fixedAskTime().Add(askAnswerDelay),
		IdempotencyKey: "idem-answer-" + query.MessageID,
		Trace: model.Trace{
			CorrelationID:   query.Trace.CorrelationID,
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed,
		},
		Security: model.Security{
			Scheme: model.SecuritySchemeEd25519,
			Nonce:  "nonce-answer-" + query.MessageID,
		},
	}
	signed, err := model.SignEnvelope(answer, cfg.answerPrivate)
	if err != nil {
		t.Fatalf("SignEnvelope(answer) error = %v", err)
	}
	answerBody, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("Marshal(answer) error = %v", err)
	}

	sendBody, err := json.Marshal(map[string]any{
		"message_id":            signed.MessageID,
		"sender_session_id":     cfg.answererSession,
		"sender_participant_id": cfg.answererAgent,
		"target_participant_id": query.From,
		"body":                  string(answerBody),
	})
	if err != nil {
		t.Fatalf("Marshal(answer send) error = %v", err)
	}

	request, err := http.NewRequest(http.MethodPost, baseURL+"/v0/agents/messages/send", bytes.NewReader(sendBody))
	if err != nil {
		t.Fatalf("NewRequest(answer send) error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+cfg.operatorToken)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return
	}
	defer func() { _ = response.Body.Close() }()
}
