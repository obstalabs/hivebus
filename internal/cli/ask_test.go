package cli

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

func TestAskCommandBuildsSignedReadOnlyRoundTrip(t *testing.T) {
	withDeterministicAskRuntime(t)

	cmd := newAskCommand()
	cmd.SetArgs([]string{"--to", "workledger/agent", "--from", "architect/agent", "--type", "canonical_repo"})
	cmd.SetIn(strings.NewReader("which workledger checkout is canonical and what's HEAD?\n"))

	var output bytes.Buffer
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("ask command error = %v", err)
	}

	var exchange askExchange
	if err := json.Unmarshal(output.Bytes(), &exchange); err != nil {
		t.Fatalf("json.Unmarshal() exchange error = %v", err)
	}

	queryPublicKey := decodeAskPublicKey(t, exchange.QueryPublicKey)
	answerPublicKey := decodeAskPublicKey(t, exchange.AnswerPublicKey)

	if err := model.VerifyEnvelope(exchange.Query, queryPublicKey); err != nil {
		t.Fatalf("VerifyEnvelope(query) error = %v", err)
	}
	if err := model.VerifyEnvelope(exchange.Answer, answerPublicKey); err != nil {
		t.Fatalf("VerifyEnvelope(answer) error = %v", err)
	}

	if exchange.Query.Type != model.MessageTypeQuery {
		t.Fatalf("query type = %q, want %q", exchange.Query.Type, model.MessageTypeQuery)
	}
	if exchange.Answer.Type != model.MessageTypeAnswer {
		t.Fatalf("answer type = %q, want %q", exchange.Answer.Type, model.MessageTypeAnswer)
	}
	if exchange.Answer.ReplyTo != exchange.Query.MessageID {
		t.Fatalf("answer reply_to = %q, want %q", exchange.Answer.ReplyTo, exchange.Query.MessageID)
	}
	if exchange.Answer.ThreadID != exchange.Query.ThreadID {
		t.Fatalf("answer thread_id = %q, want %q", exchange.Answer.ThreadID, exchange.Query.ThreadID)
	}
	if got := exchange.Answer.To; len(got) != 1 || got[0] != "architect/agent" {
		t.Fatalf("answer to = %#v, want architect/agent", got)
	}

	var query askQueryPayload
	if err := json.Unmarshal(exchange.Query.Payload, &query); err != nil {
		t.Fatalf("json.Unmarshal() query payload error = %v", err)
	}
	if !query.ReadOnly {
		t.Fatal("query payload is not read-only")
	}
	if strings.Contains(string(exchange.Query.Payload), "requested_action") {
		t.Fatal("query payload includes requested_action")
	}

	var answer askAnswerPayload
	if err := json.Unmarshal(exchange.Answer.Payload, &answer); err != nil {
		t.Fatalf("json.Unmarshal() answer payload error = %v", err)
	}
	if !strings.Contains(answer.Answer, "canonical checkout answer") {
		t.Fatalf("answer = %q, want canonical checkout regression response", answer.Answer)
	}

	if got := exchange.DeferredFollowups; len(got) != len(askDeferredFollowups) {
		t.Fatalf("deferred followups = %#v, want %#v", got, askDeferredFollowups)
	}
}

func TestBuildFixtureAnswerRejectsUnsignedAndTamperedQuery(t *testing.T) {
	exchange := buildDeterministicAskExchange(t)
	queryPublicKey := decodeAskPublicKey(t, exchange.QueryPublicKey)
	_, answerPrivateKey := deterministicAskSigningKey(2)
	random := newCountingReader()

	unsigned := exchange.Query
	unsigned.Security.Signed = false
	if _, err := buildFixtureAnswer(unsigned, queryPublicKey, answerPrivateKey, "workledger/agent", fixedAskTime().Add(askAnswerDelay), random); err == nil {
		t.Fatal("buildFixtureAnswer() expected unsigned query error")
	}

	tampered := exchange.Query
	tampered.Payload = json.RawMessage(`{"question":"tampered","question_type":"canonical_repo","read_only":true}`)
	if _, err := buildFixtureAnswer(tampered, queryPublicKey, answerPrivateKey, "workledger/agent", fixedAskTime().Add(askAnswerDelay), random); err == nil {
		t.Fatal("buildFixtureAnswer() expected tampered query error")
	}

	answerPublicKey := decodeAskPublicKey(t, exchange.AnswerPublicKey)
	tamperedAnswer := exchange.Answer
	tamperedAnswer.Payload = json.RawMessage(`{"answer":"tampered","answered_by":"workledger/agent","question_type":"canonical_repo","read_only":true}`)
	if err := model.VerifyEnvelope(tamperedAnswer, answerPublicKey); err == nil {
		t.Fatal("VerifyEnvelope(answer) expected tamper error")
	}
}

func TestAskCommandPostsSignedQueryAndVerifiesLiveAnswer(t *testing.T) {
	withDeterministicAskRuntime(t)

	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(7)
	var sentRequest askSendAgentMessageRequest
	var sentQuery model.Envelope

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/messages/send":
			if got := r.Header.Get("Authorization"); got != "Bearer operator-token" {
				t.Fatalf("Authorization = %q, want bearer token", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&sentRequest); err != nil {
				t.Fatalf("Decode(send request) error = %v", err)
			}
			if sentRequest.Body == "" {
				t.Fatal("send request body is empty")
			}
			if err := json.Unmarshal([]byte(sentRequest.Body), &sentQuery); err != nil {
				t.Fatalf("Unmarshal(query body) error = %v", err)
			}
			queryPublicKey, err := parseAskPublicKey(sentRequest.QueryPublicKey)
			if err != nil {
				t.Fatalf("parse query public key error = %v", err)
			}
			if err := model.VerifyEnvelope(sentQuery, queryPublicKey); err != nil {
				t.Fatalf("VerifyEnvelope(sent query) error = %v", err)
			}
			writeJSONResponse(t, w, askSendAgentMessageResponse{Status: "accepted"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/inbox"):
			answer := signLiveAskAnswer(
				t,
				sentQuery,
				json.RawMessage(`{"answer":"live answer","answered_by":"workledger/agent","question_type":"canonical_repo","read_only":true}`),
				answerPrivateKey,
			)
			answerBody, err := json.Marshal(answer)
			if err != nil {
				t.Fatalf("Marshal(answer) error = %v", err)
			}
			writeJSONResponse(t, w, map[string][]map[string]string{
				"messages": {{"body": string(answerBody)}},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", server.URL,
		"--to", "workledger/agent",
		"--from", "architect/agent",
		"--type", "canonical_repo",
		"--operator-token", "operator-token",
		"--answer-public-key", base64.StdEncoding.EncodeToString(answerPublicKey),
	})
	cmd.SetIn(strings.NewReader("which workledger checkout is canonical and what's HEAD?\n"))

	var output bytes.Buffer
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("ask command error = %v", err)
	}

	var exchange askExchange
	if err := json.Unmarshal(output.Bytes(), &exchange); err != nil {
		t.Fatalf("json.Unmarshal() exchange error = %v", err)
	}
	if exchange.Query.MessageID != sentQuery.MessageID {
		t.Fatalf("query message_id = %q, want sent query %q", exchange.Query.MessageID, sentQuery.MessageID)
	}
	if exchange.Answer.Type != model.MessageTypeAnswer {
		t.Fatalf("answer type = %q, want %q", exchange.Answer.Type, model.MessageTypeAnswer)
	}
	if exchange.Answer.ReplyTo != exchange.Query.MessageID {
		t.Fatalf("answer reply_to = %q, want %q", exchange.Answer.ReplyTo, exchange.Query.MessageID)
	}
	if exchange.Answer.ThreadID != exchange.Query.ThreadID {
		t.Fatalf("answer thread_id = %q, want %q", exchange.Answer.ThreadID, exchange.Query.ThreadID)
	}
	if exchange.QueryPublicKey != sentRequest.QueryPublicKey {
		t.Fatalf("query_public_key output != send request key")
	}
}

func TestAskCommandLiveTimeoutDoesNotFabricateAnswer(t *testing.T) {
	withDeterministicAskRuntime(t)

	answerPublicKey, _ := deterministicAskSigningKey(8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/messages/send":
			writeJSONResponse(t, w, askSendAgentMessageResponse{Status: "accepted"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/inbox"):
			writeJSONResponse(t, w, map[string][]model.Envelope{"messages": []model.Envelope{}})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", server.URL,
		"--to", "workledger/agent",
		"--timeout", "0s",
		"--poll-interval", "0s",
		"--answer-public-key", base64.StdEncoding.EncodeToString(answerPublicKey),
	})
	cmd.SetIn(strings.NewReader("which workledger checkout is canonical?\n"))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("ask command expected no-live-answerer error")
	}
	if !strings.Contains(err.Error(), "no live answerer") {
		t.Fatalf("ask command error = %q, want no-live-answerer", err)
	}
}

func TestAskCommandRejectsUnverifiedLiveAnswer(t *testing.T) {
	withDeterministicAskRuntime(t)

	answerPublicKey, _ := deterministicAskSigningKey(9)
	_, wrongAnswerPrivateKey := deterministicAskSigningKey(10)
	var sentQuery model.Envelope

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/messages/send":
			var request askSendAgentMessageRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("Decode(send request) error = %v", err)
			}
			if err := json.Unmarshal([]byte(request.Body), &sentQuery); err != nil {
				t.Fatalf("Unmarshal(query body) error = %v", err)
			}
			writeJSONResponse(t, w, askSendAgentMessageResponse{Status: "accepted"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/inbox"):
			answer := signLiveAskAnswer(
				t,
				sentQuery,
				json.RawMessage(`{"answer":"forged","answered_by":"workledger/agent","question_type":"direct","read_only":true}`),
				wrongAnswerPrivateKey,
			)
			answerBody, err := json.Marshal(answer)
			if err != nil {
				t.Fatalf("Marshal(answer) error = %v", err)
			}
			writeJSONResponse(t, w, map[string][]map[string]string{
				"messages": {{"body": string(answerBody)}},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", server.URL,
		"--to", "workledger/agent",
		"--answer-public-key", base64.StdEncoding.EncodeToString(answerPublicKey),
	})
	cmd.SetIn(strings.NewReader("who owns this?\n"))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("ask command expected verification error")
	}
	if !strings.Contains(err.Error(), "answer signature verification failed") {
		t.Fatalf("ask command error = %q, want verification failure", err)
	}
}

func TestAskCommandSurfacesUnsupportedQueryClassAnswer(t *testing.T) {
	withDeterministicAskRuntime(t)

	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(11)
	var sentQuery model.Envelope

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/messages/send":
			var request askSendAgentMessageRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("Decode(send request) error = %v", err)
			}
			if err := json.Unmarshal([]byte(request.Body), &sentQuery); err != nil {
				t.Fatalf("Unmarshal(query body) error = %v", err)
			}
			writeJSONResponse(t, w, askSendAgentMessageResponse{Status: "accepted"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/inbox"):
			answer := signLiveAskAnswer(
				t,
				sentQuery,
				json.RawMessage(`{"unsupported_query_class":"canonical_repo","question_type":"canonical_repo","read_only":true}`),
				answerPrivateKey,
			)
			answerBody, err := json.Marshal(answer)
			if err != nil {
				t.Fatalf("Marshal(answer) error = %v", err)
			}
			writeJSONResponse(t, w, map[string][]map[string]string{
				"messages": {{"body": string(answerBody)}},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", server.URL,
		"--to", "workledger/agent",
		"--type", "canonical_repo",
		"--answer-public-key", base64.StdEncoding.EncodeToString(answerPublicKey),
	})
	cmd.SetIn(strings.NewReader("which workledger checkout is canonical?\n"))

	var output bytes.Buffer
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("ask command error = %v", err)
	}
	if !strings.Contains(output.String(), "unsupported_query_class") {
		t.Fatalf("ask output = %s, want unsupported_query_class payload", output.String())
	}
}

func TestValidateAskQueryReadOnlyRejectsExecutableFields(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "requested_action",
			payload: `{"question":"do this","question_type":"direct","read_only":true,"requested_action":"mutate"}`,
		},
		{
			name:    "executable",
			payload: `{"question":"run this","question_type":"direct","read_only":true,"executable":"rm -rf"}`,
		},
		{
			name:    "not_read_only",
			payload: `{"question":"tell me","question_type":"direct","read_only":false}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := model.Envelope{
				Type:    model.MessageTypeQuery,
				Payload: json.RawMessage(test.payload),
			}

			if err := validateAskQueryReadOnly(envelope); err == nil {
				t.Fatal("validateAskQueryReadOnly() expected error")
			}
		})
	}
}

func TestReadAskQuestionRejectsOversizedInput(t *testing.T) {
	oversized := strings.NewReader(strings.Repeat("x", askMaxQuestionBytes+1))

	if _, err := readAskQuestion(oversized); err == nil {
		t.Fatal("readAskQuestion() expected size error")
	}
}

func buildDeterministicAskExchange(t *testing.T) askExchange {
	t.Helper()

	exchange, err := buildAskExchange(
		askOptions{from: "architect/agent", to: "workledger/agent", questionType: "canonical_repo"},
		"which workledger checkout is canonical and what's HEAD?",
		fixedAskTime(),
		newCountingReader(),
	)
	if err != nil {
		t.Fatalf("buildAskExchange() error = %v", err)
	}

	return exchange
}

func withDeterministicAskRuntime(t *testing.T) {
	t.Helper()

	oldNow := askNow
	oldRandomReader := askRandomReader
	askNow = fixedAskTime
	askRandomReader = newCountingReader()

	t.Cleanup(func() {
		askNow = oldNow
		askRandomReader = oldRandomReader
	})
}

func fixedAskTime() time.Time {
	return time.Date(2026, time.June, 5, 14, 0, 0, 0, time.UTC)
}

func decodeAskPublicKey(t *testing.T, encoded string) ed25519.PublicKey {
	t.Helper()

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode public key error = %v", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		t.Fatalf("public key size = %d, want %d", len(decoded), ed25519.PublicKeySize)
	}

	return ed25519.PublicKey(decoded)
}

func deterministicAskSigningKey(seedByte byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := bytes.Repeat([]byte{seedByte}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	return privateKey.Public().(ed25519.PublicKey), privateKey
}

func signLiveAskAnswer(
	t *testing.T,
	query model.Envelope,
	payload json.RawMessage,
	privateKey ed25519.PrivateKey,
) model.Envelope {
	t.Helper()

	answer := model.Envelope{
		MessageID:      "answer-live",
		ThreadID:       query.ThreadID,
		From:           "workledger/agent",
		To:             []string{query.From},
		Type:           model.MessageTypeAnswer,
		Payload:        payload,
		ReplyTo:        query.MessageID,
		SentAt:         fixedAskTime().Add(askAnswerDelay),
		IdempotencyKey: "idem-answer-live",
		Trace: model.Trace{
			CorrelationID:   query.Trace.CorrelationID,
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed,
		},
		Security: model.Security{
			Scheme: model.SecuritySchemeEd25519,
			Nonce:  "nonce-answer-live",
		},
	}

	signed, err := model.SignEnvelope(answer, privateKey)
	if err != nil {
		t.Fatalf("SignEnvelope(answer) error = %v", err)
	}

	return signed
}

func writeJSONResponse(t *testing.T, writer http.ResponseWriter, payload any) {
	t.Helper()

	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		t.Fatalf("Encode(response) error = %v", err)
	}
}

type countingReader struct {
	next byte
}

func newCountingReader() *countingReader {
	return &countingReader{next: 1}
}

func (reader *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = reader.next
		reader.next++
	}

	return len(p), nil
}
