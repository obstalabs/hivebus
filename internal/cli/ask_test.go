package cli

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
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
