package cli

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
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
	if !exchange.Delivered {
		t.Fatal("delivered = false, want true")
	}
	if exchange.Answers != 1 {
		t.Fatalf("answers = %d, want 1", exchange.Answers)
	}
	if exchange.ResponseStatus != askResponseStatusAnswered {
		t.Fatalf("response_status = %q, want %q", exchange.ResponseStatus, askResponseStatusAnswered)
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
	var rawSentRequest map[string]json.RawMessage

	serverURL := withAskHTTPHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/messages/send":
			if got := r.Header.Get("Authorization"); got != "Bearer operator-token" {
				t.Fatalf("Authorization = %q, want bearer token", got)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll(send request) error = %v", err)
			}
			if err := json.Unmarshal(body, &rawSentRequest); err != nil {
				t.Fatalf("Unmarshal(raw send request) error = %v", err)
			}
			if err := json.Unmarshal(body, &sentRequest); err != nil {
				t.Fatalf("Decode(send request) error = %v", err)
			}
			for _, forbidden := range []string{"from", "to", "type", "query_public_key"} {
				if _, exists := rawSentRequest[forbidden]; exists {
					t.Fatalf("send request includes fake-only field %q", forbidden)
				}
			}
			if sentRequest.MessageID == "" {
				t.Fatal("send request message_id is empty")
			}
			if sentRequest.SenderSessionID != "asker-session" {
				t.Fatalf("sender_session_id = %q, want asker-session", sentRequest.SenderSessionID)
			}
			if sentRequest.SenderParticipantID != "architect/agent" {
				t.Fatalf("sender_participant_id = %q, want architect/agent", sentRequest.SenderParticipantID)
			}
			if sentRequest.TargetParticipantID != "workledger/agent" {
				t.Fatalf("target_participant_id = %q, want workledger/agent", sentRequest.TargetParticipantID)
			}
			if sentRequest.Body == "" {
				t.Fatal("send request body is empty")
			}
			if err := json.Unmarshal([]byte(sentRequest.Body), &sentQuery); err != nil {
				t.Fatalf("Unmarshal(query body) error = %v", err)
			}
			if sentRequest.MessageID != sentQuery.MessageID {
				t.Fatalf("send request message_id = %q, want query %q", sentRequest.MessageID, sentQuery.MessageID)
			}
			writeJSONResponse(t, w, map[string]any{
				"status": "queued",
				"message": map[string]string{
					"message_id": sentRequest.MessageID,
					"body":       sentRequest.Body,
				},
				"receipt": map[string]string{"state": "queued"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v0/agents/sessions/asker-session/inbox":
			if got := r.Header.Get("Authorization"); got != "Bearer worker-token" {
				t.Fatalf("inbox Authorization = %q, want worker bearer token", got)
			}
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
			writeJSONResponse(t, w, map[string]any{
				"status": "ok",
				"session": map[string]string{
					"session_id":     "asker-session",
					"participant_id": "architect/agent",
				},
				"messages": []map[string]any{
					{
						"message": map[string]string{
							"message_id": "answer-live",
							"body":       string(answerBody),
						},
					},
				},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", serverURL,
		"--to", "workledger/agent",
		"--from", "architect/agent",
		"--type", "canonical_repo",
		"--session-id", "asker-session",
		"--operator-token", "operator-token",
		"--worker-token", "worker-token",
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
	if !exchange.Delivered {
		t.Fatal("delivered = false, want true")
	}
	if exchange.Recipient != "workledger/agent" {
		t.Fatalf("recipient = %q, want workledger/agent", exchange.Recipient)
	}
	if exchange.QueryMessageID != sentQuery.MessageID {
		t.Fatalf("query_message_id = %q, want sent query %q", exchange.QueryMessageID, sentQuery.MessageID)
	}
	if exchange.Answers != 1 {
		t.Fatalf("answers = %d, want 1", exchange.Answers)
	}
	if exchange.ResponseStatus != askResponseStatusAnswered {
		t.Fatalf("response_status = %q, want %q", exchange.ResponseStatus, askResponseStatusAnswered)
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
	queryPublicKey := decodeAskPublicKey(t, exchange.QueryPublicKey)
	if err := model.VerifyEnvelope(sentQuery, queryPublicKey); err != nil {
		t.Fatalf("VerifyEnvelope(sent query) error = %v", err)
	}
}

func TestAskCommandInsecureSelfRegistersSessionBeforeSend(t *testing.T) {
	withDeterministicAskRuntime(t)

	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(26)
	answerKeyFile := t.TempDir() + "/answer.pub"
	if err := writeAnswerPublicKeyFile(answerKeyFile, base64.StdEncoding.EncodeToString(answerPublicKey)); err != nil {
		t.Fatalf("writeAnswerPublicKeyFile() error = %v", err)
	}
	var sentQuery model.Envelope
	var requestOrder []string

	serverURL := withAskHTTPHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/sessions/register":
			requestOrder = append(requestOrder, "register")
			if got := r.Header.Get("Authorization"); got != "" {
				t.Fatalf("register Authorization = %q, want empty for tokenless insecure ask", got)
			}
			var payload model.AgentSessionPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode(register payload) error = %v", err)
			}
			if payload.AgentID != "asker-session" {
				t.Fatalf("register agent_id = %q, want asker-session", payload.AgentID)
			}
			if payload.InstallationID != "local" {
				t.Fatalf("register installation_id = %q, want local", payload.InstallationID)
			}
			if payload.SessionID != "asker-session" {
				t.Fatalf("register session_id = %q, want asker-session", payload.SessionID)
			}
			if payload.ParticipantID != "architect/agent" {
				t.Fatalf("register participant_id = %q, want architect/agent", payload.ParticipantID)
			}
			if payload.DeliveryMode != model.AgentDeliveryQueued {
				t.Fatalf("register delivery_mode = %q, want %q", payload.DeliveryMode, model.AgentDeliveryQueued)
			}
			if payload.SessionStatus != model.AgentSessionOnline {
				t.Fatalf("register session_status = %q, want %q", payload.SessionStatus, model.AgentSessionOnline)
			}
			wantLease := fixedAskTime().Add(askSelfRegisterLease).UTC().Format(time.RFC3339)
			if payload.LeaseExpiresAt != wantLease {
				t.Fatalf("register lease_expires_at = %q, want %q", payload.LeaseExpiresAt, wantLease)
			}
			writeJSONResponse(t, w, map[string]string{"status": "replaced"})
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/messages/send":
			requestOrder = append(requestOrder, "send")
			var request askSendAgentMessageRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("Decode(send request) error = %v", err)
			}
			if err := json.Unmarshal([]byte(request.Body), &sentQuery); err != nil {
				t.Fatalf("Unmarshal(query body) error = %v", err)
			}
			writeJSONResponse(t, w, askSendAgentMessageResponse{Status: "queued"})
		case r.Method == http.MethodGet && r.URL.Path == "/v0/agents/sessions/asker-session/inbox":
			requestOrder = append(requestOrder, "inbox")
			answer := signLiveAskAnswer(
				t,
				sentQuery,
				json.RawMessage(`{"answer":"registered live answer","answered_by":"workledger/agent","question_type":"canonical_repo","read_only":true}`),
				answerPrivateKey,
			)
			answerBody, err := json.Marshal(answer)
			if err != nil {
				t.Fatalf("Marshal(answer) error = %v", err)
			}
			writeJSONResponse(t, w, map[string]any{
				"messages": []map[string]any{
					{
						"message": map[string]string{
							"message_id": "answer-live",
							"body":       string(answerBody),
						},
					},
				},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", serverURL,
		"--insecure",
		"--to", "workledger/agent",
		"--from", "architect/agent",
		"--type", "canonical_repo",
		"--session-id", "asker-session",
		"--answer-public-key-file", answerKeyFile,
	})
	cmd.SetIn(strings.NewReader("which workledger checkout is canonical?\n"))

	var output bytes.Buffer
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("ask command error = %v", err)
	}
	if !strings.Contains(output.String(), "registered live answer") {
		t.Fatalf("ask output = %s, want registered live answer", output.String())
	}
	if got := strings.Join(requestOrder, ","); got != "register,send,inbox" {
		t.Fatalf("request order = %s, want register,send,inbox", got)
	}
}

func TestAskCommandSkipsMalformedUnrelatedInboxMessages(t *testing.T) {
	// WO-100: stale malformed queue entries must not hide a later correlated answer.
	withDeterministicAskRuntime(t)

	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(14)
	var sentQuery model.Envelope

	serverURL := withAskHTTPHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/messages/send":
			var request askSendAgentMessageRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("Decode(send request) error = %v", err)
			}
			if err := json.Unmarshal([]byte(request.Body), &sentQuery); err != nil {
				t.Fatalf("Unmarshal(query body) error = %v", err)
			}
			writeJSONResponse(t, w, askSendAgentMessageResponse{Status: "queued"})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/inbox"):
			answer := signLiveAskAnswer(
				t,
				sentQuery,
				json.RawMessage(`{"answer":"live answer after stale message","answered_by":"workledger/agent","question_type":"direct","read_only":true}`),
				answerPrivateKey,
			)
			answerBody, err := json.Marshal(answer)
			if err != nil {
				t.Fatalf("Marshal(answer) error = %v", err)
			}
			writeJSONResponse(t, w, map[string]any{
				"messages": []map[string]any{
					{
						"message": map[string]string{
							"message_id": "stale-non-envelope",
							"body":       "not-json and not an envelope",
						},
					},
					{
						"message": map[string]string{
							"message_id": "answer-live",
							"body":       string(answerBody),
						},
					},
				},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", serverURL,
		"--to", "workledger/agent",
		"--session-id", "asker-session",
		"--operator-token", "operator-token",
		"--worker-token", "worker-token",
		"--answer-public-key", base64.StdEncoding.EncodeToString(answerPublicKey),
	})
	cmd.SetIn(strings.NewReader("which workledger checkout is canonical?\n"))

	var output bytes.Buffer
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("ask command error = %v", err)
	}
	if !strings.Contains(output.String(), "live answer after stale message") {
		t.Fatalf("ask output = %s, want valid answer after malformed stale body", output.String())
	}
}

func TestAskCommandRejectsLiveAskWithoutRuntimeSessionAuth(t *testing.T) {
	answerPublicKey, _ := deterministicAskSigningKey(12)
	validOptions := askOptions{
		from:            "architect/agent",
		to:              "workledger/agent",
		questionType:    "canonical_repo",
		serverURL:       "http://127.0.0.1:8080",
		timeout:         defaultAskLiveTimeout,
		pollInterval:    defaultAskPollInterval,
		sessionID:       "asker-session",
		operatorToken:   "operator-token",
		workerToken:     "worker-token",
		answerPublicKey: base64.StdEncoding.EncodeToString(answerPublicKey),
	}

	tests := []struct {
		name    string
		mutate  func(*askOptions)
		wantErr string
	}{
		{
			name: "session_id",
			mutate: func(options *askOptions) {
				options.sessionID = ""
			},
			wantErr: "session-id is required",
		},
		{
			name: "operator_token",
			mutate: func(options *askOptions) {
				options.operatorToken = ""
			},
			wantErr: "operator-token is required",
		},
		{
			name: "worker_token",
			mutate: func(options *askOptions) {
				options.workerToken = ""
			},
			wantErr: "worker-token is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := validOptions
			test.mutate(&options)
			if err := options.validate(); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validate() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

// WO-104: --insecure relaxes the token requirement so the dogfood path matches a
// serve --auth-disabled server, but session-id and answer-public-key stay required.
func TestAskCommandInsecureAllowsTokenlessLiveAsk(t *testing.T) {
	answerPublicKey, _ := deterministicAskSigningKey(12)
	base := askOptions{
		from:            "architect/agent",
		to:              "workledger/agent",
		questionType:    "canonical_repo",
		serverURL:       "http://127.0.0.1:8080",
		timeout:         defaultAskLiveTimeout,
		pollInterval:    defaultAskPollInterval,
		sessionID:       "asker-session",
		answerPublicKey: base64.StdEncoding.EncodeToString(answerPublicKey),
		insecure:        true,
	}

	if err := base.validate(); err != nil {
		t.Fatalf("insecure tokenless validate() error = %v, want nil", err)
	}

	withToken := base
	withToken.operatorToken = "operator-token"
	if err := withToken.validate(); err != nil {
		t.Fatalf("insecure with token validate() error = %v, want nil", err)
	}

	missingSession := base
	missingSession.sessionID = ""
	if err := missingSession.validate(); err == nil || !strings.Contains(err.Error(), "session-id is required") {
		t.Fatalf("insecure missing session validate() error = %v, want session-id required", err)
	}

	missingKey := base
	missingKey.answerPublicKey = ""
	if err := missingKey.validate(); err == nil || !strings.Contains(err.Error(), "answer-public-key is required") {
		t.Fatalf("insecure missing answer-public-key validate() error = %v, want answer-public-key required", err)
	}
}

// WO-107: cover the actual --insecure command path and live transport headers.
func TestAskCommandInsecureLiveAskAuthorizationHeaders(t *testing.T) {
	tests := []struct {
		name                   string
		operatorToken          string
		workerToken            string
		wantSendAuthorization  string
		wantInboxAuthorization string
	}{
		{
			name: "tokenless",
		},
		{
			name:                   "provided_tokens",
			operatorToken:          "operator-token",
			workerToken:            "worker-token",
			wantSendAuthorization:  "Bearer operator-token",
			wantInboxAuthorization: "Bearer worker-token",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withDeterministicAskRuntime(t)

			answerPublicKey, answerPrivateKey := deterministicAskSigningKey(25)
			var sentQuery model.Envelope

			serverURL := withAskHTTPHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/sessions/register":
					if got := r.Header.Get("Authorization"); got != test.wantInboxAuthorization {
						t.Fatalf("register Authorization = %q, want %q", got, test.wantInboxAuthorization)
					}
					writeJSONResponse(t, w, map[string]string{"status": "registered"})
				case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/messages/send":
					if got := r.Header.Get("Authorization"); got != test.wantSendAuthorization {
						t.Fatalf("send Authorization = %q, want %q", got, test.wantSendAuthorization)
					}
					var request askSendAgentMessageRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Fatalf("Decode(send request) error = %v", err)
					}
					if err := json.Unmarshal([]byte(request.Body), &sentQuery); err != nil {
						t.Fatalf("Unmarshal(query body) error = %v", err)
					}
					writeJSONResponse(t, w, askSendAgentMessageResponse{Status: "queued"})
				case r.Method == http.MethodGet && r.URL.Path == "/v0/agents/sessions/asker-session/inbox":
					if got := r.Header.Get("Authorization"); got != test.wantInboxAuthorization {
						t.Fatalf("inbox Authorization = %q, want %q", got, test.wantInboxAuthorization)
					}
					answer := signLiveAskAnswer(
						t,
						sentQuery,
						json.RawMessage(`{"answer":"insecure live answer","answered_by":"workledger/agent","question_type":"canonical_repo","read_only":true}`),
						answerPrivateKey,
					)
					answerBody, err := json.Marshal(answer)
					if err != nil {
						t.Fatalf("Marshal(answer) error = %v", err)
					}
					writeJSONResponse(t, w, map[string]any{
						"messages": []map[string]any{
							{
								"message": map[string]string{
									"message_id": "answer-live",
									"body":       string(answerBody),
								},
							},
						},
					})
				default:
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			}))

			args := []string{
				"--server", serverURL,
				"--insecure",
				"--to", "workledger/agent",
				"--from", "architect/agent",
				"--type", "canonical_repo",
				"--session-id", "asker-session",
				"--answer-public-key", base64.StdEncoding.EncodeToString(answerPublicKey),
			}
			if test.operatorToken != "" {
				args = append(args, "--operator-token", test.operatorToken)
			}
			if test.workerToken != "" {
				args = append(args, "--worker-token", test.workerToken)
			}

			cmd := newAskCommand()
			cmd.SetArgs(args)
			cmd.SetIn(strings.NewReader("which workledger checkout is canonical?\n"))

			var output bytes.Buffer
			cmd.SetOut(&output)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("ask command error = %v", err)
			}
			if !strings.Contains(output.String(), "insecure live answer") {
				t.Fatalf("ask output = %s, want insecure live answer", output.String())
			}
		})
	}
}

func TestAskCommandLiveSendResponseObjectStillFailsRejectedStatus(t *testing.T) {
	withDeterministicAskRuntime(t)

	answerPublicKey, _ := deterministicAskSigningKey(13)
	serverURL := withAskHTTPHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/messages/send":
			writeJSONResponse(t, w, map[string]any{
				"status": "rejected",
				"message": map[string]string{
					"message_id": "query-rejected",
				},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", serverURL,
		"--to", "workledger/agent",
		"--session-id", "asker-session",
		"--operator-token", "operator-token",
		"--worker-token", "worker-token",
		"--answer-public-key", base64.StdEncoding.EncodeToString(answerPublicKey),
	})
	cmd.SetIn(strings.NewReader("which workledger checkout is canonical?\n"))

	err := cmd.Execute()
	if err == nil {
		t.Fatal("ask command expected rejected send status")
	}
	if !strings.Contains(err.Error(), "send live ask query failed") {
		t.Fatalf("ask command error = %q, want send failure", err)
	}
}

func TestAskCommandLiveTimeoutDoesNotFabricateAnswer(t *testing.T) {
	withDeterministicAskRuntime(t)

	answerPublicKey, _ := deterministicAskSigningKey(8)
	var sentQuery model.Envelope
	serverURL := withAskHTTPHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			writeJSONResponse(t, w, map[string][]model.Envelope{"messages": []model.Envelope{}})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", serverURL,
		"--to", "workledger/agent",
		"--timeout", "0s",
		"--poll-interval", "0s",
		"--session-id", "asker-session",
		"--operator-token", "operator-token",
		"--worker-token", "worker-token",
		"--answer-public-key", base64.StdEncoding.EncodeToString(answerPublicKey),
	})
	cmd.SetIn(strings.NewReader("which workledger checkout is canonical?\n"))

	var output bytes.Buffer
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("ask command error = %v", err)
	}
	if strings.Contains(output.String(), "no live answerer") {
		t.Fatalf("ask output = %s, want delivery report without no-live-answerer error", output.String())
	}
	var exchange askExchange
	if err := json.Unmarshal(output.Bytes(), &exchange); err != nil {
		t.Fatalf("json.Unmarshal() exchange error = %v", err)
	}
	if !exchange.Delivered {
		t.Fatal("delivered = false, want true")
	}
	if exchange.Recipient != "workledger/agent" {
		t.Fatalf("recipient = %q, want workledger/agent", exchange.Recipient)
	}
	if exchange.QueryMessageID != sentQuery.MessageID {
		t.Fatalf("query_message_id = %q, want sent query %q", exchange.QueryMessageID, sentQuery.MessageID)
	}
	if exchange.Answers != 0 {
		t.Fatalf("answers = %d, want 0", exchange.Answers)
	}
	if exchange.ResponseStatus != askResponseStatusNoAnswer {
		t.Fatalf("response_status = %q, want %q", exchange.ResponseStatus, askResponseStatusNoAnswer)
	}
	if exchange.VerificationError != "" {
		t.Fatalf("verification_error = %q, want empty", exchange.VerificationError)
	}
	if exchange.Answer.Type != "" {
		t.Fatalf("answer type = %q, want empty", exchange.Answer.Type)
	}
}

func TestAskCommandRejectsUnverifiedLiveAnswer(t *testing.T) {
	withDeterministicAskRuntime(t)

	answerPublicKey, _ := deterministicAskSigningKey(9)
	_, wrongAnswerPrivateKey := deterministicAskSigningKey(10)
	var sentQuery model.Envelope

	serverURL := withAskHTTPHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", serverURL,
		"--to", "workledger/agent",
		"--session-id", "asker-session",
		"--operator-token", "operator-token",
		"--worker-token", "worker-token",
		"--answer-public-key", base64.StdEncoding.EncodeToString(answerPublicKey),
	})
	cmd.SetIn(strings.NewReader("who owns this?\n"))

	var output bytes.Buffer
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("ask command error = %v", err)
	}
	var exchange askExchange
	if err := json.Unmarshal(output.Bytes(), &exchange); err != nil {
		t.Fatalf("json.Unmarshal() exchange error = %v", err)
	}
	if !exchange.Delivered {
		t.Fatal("delivered = false, want true")
	}
	if exchange.Answers != 0 {
		t.Fatalf("answers = %d, want 0", exchange.Answers)
	}
	if exchange.ResponseStatus != askResponseStatusUnverified {
		t.Fatalf("response_status = %q, want %q", exchange.ResponseStatus, askResponseStatusUnverified)
	}
	if !strings.Contains(exchange.VerificationError, "answer signature verification failed") {
		t.Fatalf("verification_error = %q, want signature verification failure", exchange.VerificationError)
	}
	if strings.Contains(output.String(), "forged") {
		t.Fatalf("ask output = %s, want forged answer withheld", output.String())
	}
}

func TestAskCommandPrefersValidAnswerOverEarlierUnverifiedCandidate(t *testing.T) {
	// WO-110: a bad candidate in the same inbox batch must not mask a later valid answer.
	withDeterministicAskRuntime(t)

	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(27)
	_, wrongAnswerPrivateKey := deterministicAskSigningKey(28)
	var sentQuery model.Envelope

	serverURL := withAskHTTPHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			forged := signLiveAskAnswer(
				t,
				sentQuery,
				json.RawMessage(`{"answer":"forged","answered_by":"workledger/agent","question_type":"direct","read_only":true}`),
				wrongAnswerPrivateKey,
			)
			trusted := signLiveAskAnswer(
				t,
				sentQuery,
				json.RawMessage(`{"answer":"trusted","answered_by":"workledger/agent","question_type":"direct","read_only":true}`),
				answerPrivateKey,
			)
			forgedBody, err := json.Marshal(forged)
			if err != nil {
				t.Fatalf("Marshal(forged answer) error = %v", err)
			}
			trustedBody, err := json.Marshal(trusted)
			if err != nil {
				t.Fatalf("Marshal(trusted answer) error = %v", err)
			}
			writeJSONResponse(t, w, map[string][]map[string]string{
				"messages": {
					{"body": string(forgedBody)},
					{"body": string(trustedBody)},
				},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", serverURL,
		"--to", "workledger/agent",
		"--session-id", "asker-session",
		"--operator-token", "operator-token",
		"--worker-token", "worker-token",
		"--answer-public-key", base64.StdEncoding.EncodeToString(answerPublicKey),
	})
	cmd.SetIn(strings.NewReader("who owns this?\n"))

	var output bytes.Buffer
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("ask command error = %v", err)
	}
	var exchange askExchange
	if err := json.Unmarshal(output.Bytes(), &exchange); err != nil {
		t.Fatalf("json.Unmarshal() exchange error = %v", err)
	}
	if !exchange.Delivered {
		t.Fatal("delivered = false, want true")
	}
	if exchange.Answers != 1 {
		t.Fatalf("answers = %d, want 1", exchange.Answers)
	}
	if exchange.ResponseStatus != askResponseStatusAnswered {
		t.Fatalf("response_status = %q, want %q", exchange.ResponseStatus, askResponseStatusAnswered)
	}
	if exchange.VerificationError != "" {
		t.Fatalf("verification_error = %q, want empty", exchange.VerificationError)
	}
	if !strings.Contains(output.String(), "trusted") {
		t.Fatalf("ask output = %s, want trusted answer", output.String())
	}
	if strings.Contains(output.String(), "forged") {
		t.Fatalf("ask output = %s, want forged answer withheld", output.String())
	}
}

func TestValidateLiveAskAnswerRejectsWrongRoute(t *testing.T) {
	// WO-101: cryptographic validity is not enough without route coherence.
	withDeterministicAskRuntime(t)

	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(15)
	query, err := buildSignedAskQuery(
		askOptions{from: "architect/agent", to: "workledger/agent", questionType: "direct"},
		"who owns this?",
		fixedAskTime(),
		mustAskPrivateKey(t, 16),
		newCountingReader(),
	)
	if err != nil {
		t.Fatalf("buildSignedAskQuery() error = %v", err)
	}

	tests := []struct {
		name      string
		from      string
		to        []string
		recipient string
		wantErr   string
	}{
		{
			name:    "wrong_from",
			from:    "other/agent",
			to:      []string{"architect/agent"},
			wantErr: "answer from",
		},
		{
			name:      "missing_to",
			from:      "workledger/agent",
			to:        nil,
			recipient: "architect/agent",
			wantErr:   "answer to",
		},
		{
			name:    "multiple_to",
			from:    "workledger/agent",
			to:      []string{"architect/agent", "other/agent"},
			wantErr: "answer to",
		},
		{
			name:    "wrong_to",
			from:    "workledger/agent",
			to:      []string{"other/agent"},
			wantErr: "answer to",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			answer := signLiveAskAnswerWithRoute(
				t,
				query,
				json.RawMessage(`{"answer":"route mismatch","answered_by":"workledger/agent","question_type":"direct","read_only":true}`),
				test.from,
				test.to,
				test.recipient,
				answerPrivateKey,
			)

			err := validateLiveAskAnswer(query, answer, answerPublicKey, fixedAskTime().Add(askAnswerDelay))
			if err == nil {
				t.Fatal("validateLiveAskAnswer() expected route error")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateLiveAskAnswer() error = %q, want %q", err, test.wantErr)
			}
			if strings.Contains(err.Error(), "signature verification") {
				t.Fatalf("validateLiveAskAnswer() error = %q, want route failure not signature failure", err)
			}
		})
	}
}

func TestValidateLiveAskAnswerAcceptsCanonicalTargetedRecipient(t *testing.T) {
	// WO-102: canonical targeted answers may address the asker via Recipient.
	withDeterministicAskRuntime(t)

	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(17)
	query, err := buildSignedAskQuery(
		askOptions{from: "architect/agent", to: "workledger/agent", questionType: "direct"},
		"who owns this?",
		fixedAskTime(),
		mustAskPrivateKey(t, 18),
		newCountingReader(),
	)
	if err != nil {
		t.Fatalf("buildSignedAskQuery() error = %v", err)
	}

	answer := signLiveAskAnswerWithRouteAndScope(
		t,
		query,
		json.RawMessage(`{"answer":"canonical route","answered_by":"workledger/agent","question_type":"direct","read_only":true}`),
		"workledger/agent",
		nil,
		"architect/agent",
		model.ScopeTargeted,
		answerPrivateKey,
	)

	if err := validateLiveAskAnswer(query, answer, answerPublicKey, fixedAskTime().Add(askAnswerDelay)); err != nil {
		t.Fatalf("validateLiveAskAnswer() error = %v", err)
	}
}

func TestValidateLiveAskAnswerAcceptsCanonicalTargetedQueryRecipient(t *testing.T) {
	// WO-105: canonical targeted queries may identify the target without legacy To.
	withDeterministicAskRuntime(t)

	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(21)
	query := signTargetedLiveAskQuery(
		t,
		"architect/agent",
		nil,
		"workledger/agent",
		mustAskPrivateKey(t, 22),
	)
	answer := signLiveAskAnswerWithRouteAndScope(
		t,
		query,
		json.RawMessage(`{"answer":"canonical query route","answered_by":"workledger/agent","question_type":"direct","read_only":true}`),
		"workledger/agent",
		nil,
		"architect/agent",
		model.ScopeTargeted,
		answerPrivateKey,
	)

	if err := validateLiveAskAnswer(query, answer, answerPublicKey, fixedAskTime().Add(askAnswerDelay)); err != nil {
		t.Fatalf("validateLiveAskAnswer() error = %v", err)
	}
}

func TestValidateLiveAskAnswerRejectsCanonicalRecipientRouteMismatch(t *testing.T) {
	// WO-102: signed canonical recipient fields are part of answer route trust.
	withDeterministicAskRuntime(t)

	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(19)
	query, err := buildSignedAskQuery(
		askOptions{from: "architect/agent", to: "workledger/agent", questionType: "direct"},
		"who owns this?",
		fixedAskTime(),
		mustAskPrivateKey(t, 20),
		newCountingReader(),
	)
	if err != nil {
		t.Fatalf("buildSignedAskQuery() error = %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(model.Envelope) model.Envelope
		wantErr string
	}{
		{
			name: "wrong_recipient",
			mutate: func(answer model.Envelope) model.Envelope {
				return signLiveAskAnswerWithRouteAndScope(
					t,
					query,
					answer.Payload,
					"workledger/agent",
					nil,
					"other/agent",
					model.ScopeTargeted,
					answerPrivateKey,
				)
			},
			wantErr: "answer recipient",
		},
		{
			name: "missing_recipient",
			mutate: func(answer model.Envelope) model.Envelope {
				answer.Recipient = ""
				return answer
			},
			wantErr: "answer recipient",
		},
		{
			name: "legacy_to_mirror_mismatch",
			mutate: func(answer model.Envelope) model.Envelope {
				answer.To = []string{"other/agent"}
				return answer
			},
			wantErr: "answer to",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validAnswer := signLiveAskAnswerWithRouteAndScope(
				t,
				query,
				json.RawMessage(`{"answer":"canonical route mismatch","answered_by":"workledger/agent","question_type":"direct","read_only":true}`),
				"workledger/agent",
				[]string{"architect/agent"},
				"architect/agent",
				model.ScopeTargeted,
				answerPrivateKey,
			)
			answer := test.mutate(validAnswer)

			err := validateLiveAskAnswer(query, answer, answerPublicKey, fixedAskTime().Add(askAnswerDelay))
			if err == nil {
				t.Fatal("validateLiveAskAnswer() expected route error")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateLiveAskAnswer() error = %q, want %q", err, test.wantErr)
			}
			if strings.Contains(err.Error(), "signature verification") {
				t.Fatalf("validateLiveAskAnswer() error = %q, want route failure not signature failure", err)
			}
		})
	}
}

func TestValidateLiveAskAnswerRejectsCanonicalQueryRouteMismatch(t *testing.T) {
	// WO-105: query route coherence is checked before answer signature errors.
	withDeterministicAskRuntime(t)

	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(23)
	validQuery := signTargetedLiveAskQuery(
		t,
		"architect/agent",
		nil,
		"workledger/agent",
		mustAskPrivateKey(t, 24),
	)
	validAnswer := signLiveAskAnswerWithRouteAndScope(
		t,
		validQuery,
		json.RawMessage(`{"answer":"canonical query route mismatch","answered_by":"workledger/agent","question_type":"direct","read_only":true}`),
		"workledger/agent",
		nil,
		"architect/agent",
		model.ScopeTargeted,
		answerPrivateKey,
	)

	tests := []struct {
		name    string
		mutate  func(model.Envelope) model.Envelope
		wantErr string
	}{
		{
			name: "missing_recipient",
			mutate: func(query model.Envelope) model.Envelope {
				query.Recipient = ""
				return query
			},
			wantErr: "query recipient",
		},
		{
			name: "legacy_to_mirror_mismatch",
			mutate: func(query model.Envelope) model.Envelope {
				query.To = []string{"other/agent"}
				return query
			},
			wantErr: "query to",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := test.mutate(validQuery)

			err := validateLiveAskAnswer(query, validAnswer, answerPublicKey, fixedAskTime().Add(askAnswerDelay))
			if err == nil {
				t.Fatal("validateLiveAskAnswer() expected route error")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateLiveAskAnswer() error = %q, want %q", err, test.wantErr)
			}
			if strings.Contains(err.Error(), "signature verification") {
				t.Fatalf("validateLiveAskAnswer() error = %q, want route failure not signature failure", err)
			}
		})
	}
}

func TestAskCommandSurfacesUnsupportedQueryClassAnswer(t *testing.T) {
	withDeterministicAskRuntime(t)

	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(11)
	var sentQuery model.Envelope

	serverURL := withAskHTTPHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	cmd := newAskCommand()
	cmd.SetArgs([]string{
		"--server", serverURL,
		"--to", "workledger/agent",
		"--type", "canonical_repo",
		"--session-id", "asker-session",
		"--operator-token", "operator-token",
		"--worker-token", "worker-token",
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

func withAskHTTPHandler(t *testing.T, handler http.Handler) string {
	t.Helper()

	oldClient := askHTTPClient
	askHTTPClient = handlerBackedClient(handler)
	t.Cleanup(func() {
		askHTTPClient = oldClient
	})

	return "http://hivebus.test"
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

func mustAskPrivateKey(t *testing.T, seedByte byte) ed25519.PrivateKey {
	t.Helper()

	_, privateKey := deterministicAskSigningKey(seedByte)
	return privateKey
}

func signTargetedLiveAskQuery(
	t *testing.T,
	from string,
	to []string,
	recipient string,
	privateKey ed25519.PrivateKey,
) model.Envelope {
	t.Helper()

	// WO-105: build canonical targeted queries without exercising the legacy builder.
	query := model.Envelope{
		MessageID:      "query-targeted-live",
		ThreadID:       "thread-targeted-live",
		From:           from,
		To:             append([]string(nil), to...),
		Scope:          model.ScopeTargeted,
		Recipient:      recipient,
		Type:           model.MessageTypeQuery,
		Payload:        json.RawMessage(`{"question":"who owns this?","question_type":"direct","read_only":true}`),
		SentAt:         fixedAskTime(),
		IdempotencyKey: "idem-query-targeted-live",
		Trace: model.Trace{
			CorrelationID:   "corr-targeted-live",
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed,
		},
		Security: model.Security{
			Scheme: model.SecuritySchemeEd25519,
			Nonce:  "nonce-query-targeted-live",
		},
	}

	signed, err := model.SignEnvelope(query, privateKey)
	if err != nil {
		t.Fatalf("SignEnvelope(query) error = %v", err)
	}

	return signed
}

func signLiveAskAnswer(
	t *testing.T,
	query model.Envelope,
	payload json.RawMessage,
	privateKey ed25519.PrivateKey,
) model.Envelope {
	t.Helper()

	return signLiveAskAnswerWithRoute(t, query, payload, "workledger/agent", []string{query.From}, "", privateKey)
}

func signLiveAskAnswerWithRoute(
	t *testing.T,
	query model.Envelope,
	payload json.RawMessage,
	from string,
	to []string,
	recipient string,
	privateKey ed25519.PrivateKey,
) model.Envelope {
	t.Helper()

	return signLiveAskAnswerWithRouteAndScope(t, query, payload, from, to, recipient, "", privateKey)
}

func signLiveAskAnswerWithRouteAndScope(
	t *testing.T,
	query model.Envelope,
	payload json.RawMessage,
	from string,
	to []string,
	recipient string,
	scope model.Scope,
	privateKey ed25519.PrivateKey,
) model.Envelope {
	t.Helper()

	answer := model.Envelope{
		MessageID:      "answer-live",
		ThreadID:       query.ThreadID,
		From:           from,
		To:             append([]string(nil), to...),
		Scope:          scope,
		Recipient:      recipient,
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
