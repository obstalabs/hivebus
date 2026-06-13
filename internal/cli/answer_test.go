package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
)

func TestAnswerRunnerAnswersRepoStatusOverHTTPContract(t *testing.T) {
	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(31)
	queryPublicKey, queryPrivateKey := deterministicAskSigningKey(32)
	query := signedAnswerTestQuery(t, "repo_status", queryPublicKey, queryPrivateKey)
	queryBody, err := json.Marshal(query)
	if err != nil {
		t.Fatalf("Marshal(query) error = %v", err)
	}

	var registered bool
	var sentAnswer model.Envelope
	var delivered bool
	resolver := &fakeRepoStatusResolver{
		facts: repoStatusFacts{
			AgentID:           "workledger/agent",
			Project:           "hivebus",
			RepoID:            "/repo/hivebus",
			CanonicalRepoPath: "/repo/hivebus",
			Remote:            "git@example.com:obstalabs/hivebus.git",
			Branch:            "main",
			HeadShort:         "abc1234",
			HeadFull:          "abc1234567890abc1234567890abc1234567890",
			AbsoluteGitDir:    "/repo/hivebus/.git",
			GitDirDev:         11,
			GitDirIno:         22,
			Dirty:             true,
			WorktreeRole:      "primary_worktree",
			LeaseID:           "lease-1",
			WorkOrder:         "hivebus/WO-95",
		},
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/sessions/register":
			requireAnswerAuth(t, r, "Bearer worker-token")
			var payload model.AgentSessionPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode(register payload) error = %v", err)
			}
			if payload.AgentID != "workledger/agent" {
				t.Fatalf("register agent_id = %q, want workledger/agent", payload.AgentID)
			}
			if payload.ParticipantID != "workledger/agent" {
				t.Fatalf("register participant_id = %q, want workledger/agent", payload.ParticipantID)
			}
			if payload.DeliveryMode != model.AgentDeliveryQueued {
				t.Fatalf("register delivery_mode = %q, want %q", payload.DeliveryMode, model.AgentDeliveryQueued)
			}
			if payload.SessionStatus != model.AgentSessionOnline {
				t.Fatalf("register session_status = %q, want %q", payload.SessionStatus, model.AgentSessionOnline)
			}
			if payload.AnswerPublicKey != base64.StdEncoding.EncodeToString(answerPublicKey) {
				t.Fatalf("register answer_public_key = %q, want deterministic key", payload.AnswerPublicKey)
			}
			registered = true
			writeJSONResponse(t, w, map[string]string{"status": "registered"})
		case r.Method == http.MethodGet && r.URL.Path == "/v0/agents/sessions/wl-1/inbox":
			requireAnswerAuth(t, r, "Bearer worker-token")
			writeJSONResponse(t, w, map[string]any{
				"status": "ok",
				"messages": []map[string]any{
					{
						"message": map[string]string{
							"message_id": query.MessageID,
							"body":       string(queryBody),
						},
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/messages/send":
			requireAnswerAuth(t, r, "Bearer operator-token")
			var request answerSendAgentMessageRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("Decode(send payload) error = %v", err)
			}
			if request.SenderSessionID != "wl-1" {
				t.Fatalf("sender_session_id = %q, want wl-1", request.SenderSessionID)
			}
			if request.SenderParticipantID != "workledger/agent" {
				t.Fatalf("sender_participant_id = %q, want workledger/agent", request.SenderParticipantID)
			}
			if request.TargetParticipantID != "architect/session" {
				t.Fatalf("target_participant_id = %q, want architect/session", request.TargetParticipantID)
			}
			if request.TTLSeconds != 30 {
				t.Fatalf("ttl_seconds = %d, want 30", request.TTLSeconds)
			}
			if err := json.Unmarshal([]byte(request.Body), &sentAnswer); err != nil {
				t.Fatalf("Unmarshal(answer body) error = %v", err)
			}
			if err := model.VerifyEnvelope(sentAnswer, answerPublicKey); err != nil {
				t.Fatalf("VerifyEnvelope(answer) error = %v", err)
			}
			writeJSONResponse(t, w, map[string]string{"status": "queued"})
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/messages/"+query.MessageID+"/deliver":
			requireAnswerAuth(t, r, "Bearer worker-token")
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("Decode(deliver payload) error = %v", err)
			}
			if request["session_id"] != "wl-1" {
				t.Fatalf("deliver session_id = %q, want wl-1", request["session_id"])
			}
			delivered = true
			writeJSONResponse(t, w, map[string]string{"status": "delivered"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})

	var logs bytes.Buffer
	runner := answerRunner{
		options: answerOptions{
			agentID:           "workledger/agent",
			serverURL:         "http://hivebus.test",
			sessionID:         "wl-1",
			installationID:    "install-1",
			project:           "hivebus",
			repoPath:          "/repo/hivebus",
			pollInterval:      defaultAnswerPollInterval,
			heartbeatInterval: defaultAnswerHeartbeatInterval,
			leaseDuration:     defaultAnswerLeaseDuration,
			answerTTL:         defaultAnswerTTL,
			operatorToken:     "operator-token",
			workerToken:       "worker-token",
			leaseID:           "lease-1",
			workOrder:         "hivebus/WO-95",
		},
		transport: answerHTTPTransport{
			baseURL:       "http://hivebus.test",
			operatorToken: "operator-token",
			workerToken:   "worker-token",
			client:        handlerBackedClient(handler),
		},
		resolver:   resolver,
		privateKey: answerPrivateKey,
		publicKey:  answerPublicKey,
		random:     newCountingReader(),
		now:        fixedAnswerTime,
		logs:       &logs,
		answered:   make(map[string]struct{}),
	}

	if err := runner.register(context.Background()); err != nil {
		t.Fatalf("register() error = %v", err)
	}
	if err := runner.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce() error = %v", err)
	}
	if !registered {
		t.Fatal("register endpoint was not called")
	}
	if !delivered {
		t.Fatal("deliver endpoint was not called")
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
	if sentAnswer.Type != model.MessageTypeAnswer {
		t.Fatalf("answer type = %q, want %q", sentAnswer.Type, model.MessageTypeAnswer)
	}
	if sentAnswer.ReplyTo != query.MessageID {
		t.Fatalf("answer reply_to = %q, want %q", sentAnswer.ReplyTo, query.MessageID)
	}
	if sentAnswer.ThreadID != query.ThreadID {
		t.Fatalf("answer thread_id = %q, want %q", sentAnswer.ThreadID, query.ThreadID)
	}
	if sentAnswer.From != "workledger/agent" {
		t.Fatalf("answer from = %q, want workledger/agent", sentAnswer.From)
	}
	if got := sentAnswer.To; len(got) != 1 || got[0] != "architect/session" {
		t.Fatalf("answer to = %#v, want architect/session", got)
	}

	var payload repoStatusAnswerPayload
	if err := json.Unmarshal(sentAnswer.Payload, &payload); err != nil {
		t.Fatalf("Unmarshal(repo status payload) error = %v", err)
	}
	if payload.TrustClass != answerTrustClassToolAsserted {
		t.Fatalf("trust_class = %q, want %q", payload.TrustClass, answerTrustClassToolAsserted)
	}
	if payload.CanonicalRepoPath != "/repo/hivebus" {
		t.Fatalf("canonical_repo_path = %q, want /repo/hivebus", payload.CanonicalRepoPath)
	}
	if payload.RepoID != "/repo/hivebus" {
		t.Fatalf("repo_id = %q, want /repo/hivebus", payload.RepoID)
	}
	if payload.Head.Short != "abc1234" || payload.Head.Full == "" {
		t.Fatalf("head = %#v, want short/full head", payload.Head)
	}
	if payload.GitHeadSHA != payload.Head.Full {
		t.Fatalf("git_head_sha = %q, want head.full %q", payload.GitHeadSHA, payload.Head.Full)
	}
	if payload.AbsoluteGitDir != "/repo/hivebus/.git" {
		t.Fatalf("absolute_git_dir = %q, want /repo/hivebus/.git", payload.AbsoluteGitDir)
	}
	if payload.GitDirDev != 11 || payload.GitDirIno != 22 {
		t.Fatalf("git dir dev/ino = %d/%d, want 11/22", payload.GitDirDev, payload.GitDirIno)
	}
	if !payload.Dirty {
		t.Fatal("dirty = false, want true")
	}
	if payload.ObservedAt != fixedAnswerTime() {
		t.Fatalf("observed_at = %s, want fixed time", payload.ObservedAt)
	}
	if payload.ExpiresAt.Sub(payload.ObservedAt) != defaultAnswerTTL {
		t.Fatalf("expires_at - observed_at = %s, want %s", payload.ExpiresAt.Sub(payload.ObservedAt), defaultAnswerTTL)
	}
	if !strings.Contains(logs.String(), "answered query_id="+query.MessageID) {
		t.Fatalf("logs = %q, want answered audit line", logs.String())
	}
}

func TestAnswerRunnerReturnsSignedUnsupportedQueryClass(t *testing.T) {
	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(41)
	queryPublicKey, queryPrivateKey := deterministicAskSigningKey(42)
	query := signedAnswerTestQuery(t, "direct", queryPublicKey, queryPrivateKey)
	record := answerRecordForQuery(t, query)
	transport := &fakeAnswerTransport{inbox: []answerAgentMessageRecord{record}}
	resolver := &fakeRepoStatusResolver{}
	runner := fakeAnswerRunner(answerPublicKey, answerPrivateKey, transport, resolver)

	if err := runner.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce() error = %v", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls = %d, want 0 for unsupported query class", resolver.calls)
	}
	if len(transport.sent) != 1 {
		t.Fatalf("sent answers = %d, want 1", len(transport.sent))
	}
	if len(transport.delivered) != 1 || transport.delivered[0] != query.MessageID {
		t.Fatalf("delivered = %#v, want query delivered", transport.delivered)
	}

	var answer model.Envelope
	if err := json.Unmarshal([]byte(transport.sent[0].Body), &answer); err != nil {
		t.Fatalf("Unmarshal(answer) error = %v", err)
	}
	if err := model.VerifyEnvelope(answer, answerPublicKey); err != nil {
		t.Fatalf("VerifyEnvelope(answer) error = %v", err)
	}
	var payload unsupportedAnswerPayload
	if err := json.Unmarshal(answer.Payload, &payload); err != nil {
		t.Fatalf("Unmarshal(unsupported payload) error = %v", err)
	}
	if payload.UnsupportedQueryClass != "direct" {
		t.Fatalf("unsupported_query_class = %q, want direct", payload.UnsupportedQueryClass)
	}
}

func TestAnswerRunnerRefusesQueryWithoutVerificationKey(t *testing.T) {
	answerPublicKey, answerPrivateKey := deterministicAskSigningKey(51)
	_, queryPrivateKey := deterministicAskSigningKey(52)
	query := signedAnswerTestQueryWithoutPayloadKey(t, "repo_status", queryPrivateKey)
	record := answerRecordForQuery(t, query)
	transport := &fakeAnswerTransport{inbox: []answerAgentMessageRecord{record}}
	resolver := &fakeRepoStatusResolver{}
	runner := fakeAnswerRunner(answerPublicKey, answerPrivateKey, transport, resolver)

	if err := runner.pollOnce(context.Background()); err != nil {
		t.Fatalf("pollOnce() error = %v", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls = %d, want 0 for unverifiable query", resolver.calls)
	}
	if len(transport.sent) != 0 {
		t.Fatalf("sent answers = %d, want 0 for unverifiable query", len(transport.sent))
	}
	if len(transport.delivered) != 1 || transport.delivered[0] != query.MessageID {
		t.Fatalf("delivered = %#v, want unverifiable query delivered once", transport.delivered)
	}
}

func TestBuildSignedAskQueryCarriesVerificationKeyForAnswerer(t *testing.T) {
	publicKey, privateKey := deterministicAskSigningKey(61)
	query, err := buildSignedAskQuery(
		askOptions{from: "architect/session", to: "workledger/agent", questionType: "repo_status"},
		"which checkout is canonical and what is HEAD?",
		fixedAnswerTime(),
		privateKey,
		newCountingReader(),
	)
	if err != nil {
		t.Fatalf("buildSignedAskQuery() error = %v", err)
	}

	payload, parsedPublicKey, err := verifiedAnswerQuery(query)
	if err != nil {
		t.Fatalf("verifiedAnswerQuery() error = %v", err)
	}
	if payload.QueryPublicKey != base64.StdEncoding.EncodeToString(publicKey) {
		t.Fatalf("query_public_key = %q, want generated query key", payload.QueryPublicKey)
	}
	if !bytes.Equal(parsedPublicKey, publicKey) {
		t.Fatalf("parsed query key does not match generated query public key")
	}
	if err := model.VerifyEnvelope(query, parsedPublicKey); err != nil {
		t.Fatalf("VerifyEnvelope(query) error = %v", err)
	}
}

func TestGitRepoStatusResolverReadsDiskAtAnswerTime(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoPath := t.TempDir()
	runGitTestCommand(t, "", "init", repoPath)
	runGitTestCommand(t, repoPath, "checkout", "-b", "main")
	runGitTestCommand(t, repoPath, "config", "user.email", "test@example.com")
	runGitTestCommand(t, repoPath, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("hivebus\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(README) error = %v", err)
	}
	runGitTestCommand(t, repoPath, "add", "README.md")
	runGitTestCommand(t, repoPath, "commit", "-m", "initial")
	runGitTestCommand(t, repoPath, "remote", "add", "origin", "git@example.com:obstalabs/hivebus.git")
	if err := os.WriteFile(filepath.Join(repoPath, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(dirty) error = %v", err)
	}

	resolver := gitRepoStatusResolver{}
	facts, err := resolver.ResolveRepoStatus(context.Background(), repoStatusRequest{
		AgentID:   "workledger/agent",
		Project:   "hivebus",
		RepoPath:  repoPath,
		LeaseID:   "lease-1",
		WorkOrder: "hivebus/WO-95",
	})
	if err != nil {
		t.Fatalf("ResolveRepoStatus() error = %v", err)
	}
	canonicalPath, err := filepath.EvalSymlinks(repoPath)
	if err != nil {
		t.Fatalf("EvalSymlinks(repo) error = %v", err)
	}
	if facts.CanonicalRepoPath != canonicalPath {
		t.Fatalf("canonical_repo_path = %q, want %q", facts.CanonicalRepoPath, canonicalPath)
	}
	if facts.RepoID != canonicalPath {
		t.Fatalf("repo_id = %q, want %q", facts.RepoID, canonicalPath)
	}
	if facts.Remote != "git@example.com:obstalabs/hivebus.git" {
		t.Fatalf("remote = %q, want origin URL", facts.Remote)
	}
	if facts.Branch != "main" {
		t.Fatalf("branch = %q, want main", facts.Branch)
	}
	if len(facts.HeadFull) != 40 || facts.HeadShort == "" {
		t.Fatalf("head short/full = %q/%q, want git SHAs", facts.HeadShort, facts.HeadFull)
	}
	expectedGitDir := runGitTestCommand(t, repoPath, "rev-parse", "--absolute-git-dir")
	if facts.AbsoluteGitDir != expectedGitDir {
		t.Fatalf("absolute_git_dir = %q, want %q", facts.AbsoluteGitDir, expectedGitDir)
	}
	expectedDev, expectedIno := gitDirDeviceInode(expectedGitDir)
	if facts.GitDirDev != expectedDev || facts.GitDirIno != expectedIno {
		t.Fatalf("git dir dev/ino = %d/%d, want %d/%d", facts.GitDirDev, facts.GitDirIno, expectedDev, expectedIno)
	}
	if !facts.Dirty {
		t.Fatal("dirty = false, want true from untracked file")
	}
	if facts.WorktreeRole != "primary_worktree" {
		t.Fatalf("worktree_role = %q, want primary_worktree", facts.WorktreeRole)
	}
	if facts.LeaseID != "lease-1" || facts.WorkOrder != "hivebus/WO-95" {
		t.Fatalf("lease/wo = %q/%q, want provenance", facts.LeaseID, facts.WorkOrder)
	}
}

func TestRootCommandIncludesAnswerCommand(t *testing.T) {
	cmd := NewRootCommand()
	found, _, err := cmd.Find([]string{"answer"})
	if err != nil {
		t.Fatalf("Find(answer) error = %v", err)
	}
	if found == nil || found.Name() != "answer" {
		t.Fatalf("Find(answer) = %#v, want answer command", found)
	}
}

func TestAnswerCommandPrintPublicKeyExitsWithoutLoop(t *testing.T) {
	publicKey, privateKey := deterministicAskSigningKey(71)

	cmd := newAnswerCommand()
	cmd.SetArgs([]string{
		"--signing-key", base64.StdEncoding.EncodeToString(privateKey.Seed()),
		"--print-public-key",
	})

	var output bytes.Buffer
	var logs bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&logs)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("answer command error = %v", err)
	}
	if got := strings.TrimSpace(output.String()); got != base64.StdEncoding.EncodeToString(publicKey) {
		t.Fatalf("printed public key = %q, want deterministic key", got)
	}
	if logs.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", logs.String())
	}
}

func TestAnswerCommandPrintPublicKeyRequiresSigningKey(t *testing.T) {
	cmd := newAnswerCommand()
	cmd.SetArgs([]string{"--print-public-key"})

	var output bytes.Buffer
	var logs bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&logs)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("answer command succeeded, want signing-key error")
	}
	if !strings.Contains(err.Error(), "--signing-key is required with --print-public-key") {
		t.Fatalf("error = %q, want signing-key requirement", err)
	}
	if output.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", output.String())
	}
}

func TestAnswerPublicKeyFileRoundTripWithAsk(t *testing.T) {
	publicKey, privateKey := deterministicAskSigningKey(72)
	publicKeyFile := filepath.Join(t.TempDir(), "keys", "answer.pub")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v0/agents/sessions/register":
			writeJSONResponse(t, w, map[string]string{"status": "registered"})
		case r.Method == http.MethodGet && r.URL.Path == "/v0/agents/sessions/wl-1/inbox":
			writeJSONResponse(t, w, map[string]any{"status": "ok", "messages": []any{}})
			cancel()
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	oldClient := answerHTTPClient
	answerHTTPClient = handlerBackedClient(handler)
	t.Cleanup(func() {
		answerHTTPClient = oldClient
	})

	cmd := newAnswerCommand()
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{
		"--agent", "workledger/agent",
		"--server", "http://hivebus.test",
		"--session-id", "wl-1",
		"--project", "hivebus",
		"--repo", t.TempDir(),
		"--signing-key", base64.StdEncoding.EncodeToString(privateKey),
		"--public-key-file", publicKeyFile,
		"--insecure",
	})

	var output bytes.Buffer
	var logs bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&logs)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("answer command error = %v\nlogs=%s", err, logs.String())
	}

	encodedPublicKey := base64.StdEncoding.EncodeToString(publicKey)
	data, err := os.ReadFile(publicKeyFile)
	if err != nil {
		t.Fatalf("ReadFile(public key) error = %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != encodedPublicKey {
		t.Fatalf("public key file = %q, want deterministic key", got)
	}

	var startup answerStartupOutput
	if err := json.Unmarshal(output.Bytes(), &startup); err != nil {
		t.Fatalf("Unmarshal(startup) error = %v", err)
	}
	if startup.AnswerPublicKey != encodedPublicKey {
		t.Fatalf("startup answer_public_key = %q, want deterministic key", startup.AnswerPublicKey)
	}

	options := askOptions{answerKeyFile: publicKeyFile}
	options.normalize()
	if err := options.loadAnswerKeyFile(); err != nil {
		t.Fatalf("loadAnswerKeyFile() error = %v", err)
	}
	parsedPublicKey, err := parseAskPublicKey(options.answerPublicKey)
	if err != nil {
		t.Fatalf("parseAskPublicKey() error = %v", err)
	}
	if !bytes.Equal(parsedPublicKey, publicKey) {
		t.Fatalf("ask parsed public key does not match answer key file")
	}
}

type handlerRoundTripper struct {
	handler http.Handler
}

func handlerBackedClient(handler http.Handler) askHTTPDoer {
	return &http.Client{Transport: handlerRoundTripper{handler: handler}}
}

func (transport handlerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	cloned := request.Clone(request.Context())
	cloned.RequestURI = ""
	transport.handler.ServeHTTP(recorder, cloned)
	return recorder.Result(), nil
}

type fakeRepoStatusResolver struct {
	facts repoStatusFacts
	err   error
	calls int
}

func (resolver *fakeRepoStatusResolver) ResolveRepoStatus(context.Context, repoStatusRequest) (repoStatusFacts, error) {
	resolver.calls++
	if resolver.err != nil {
		return repoStatusFacts{}, resolver.err
	}

	return resolver.facts, nil
}

type fakeAnswerTransport struct {
	inbox     []answerAgentMessageRecord
	sent      []answerSendAgentMessageRequest
	delivered []string
}

func (transport *fakeAnswerTransport) RegisterSession(context.Context, model.AgentSessionPayload) error {
	return nil
}

func (transport *fakeAnswerTransport) HeartbeatSession(context.Context, model.AgentSessionPayload) error {
	return nil
}

func (transport *fakeAnswerTransport) FetchInbox(context.Context, string) ([]answerAgentMessageRecord, error) {
	return transport.inbox, nil
}

func (transport *fakeAnswerTransport) SendAnswer(_ context.Context, request answerSendAgentMessageRequest) error {
	transport.sent = append(transport.sent, request)
	return nil
}

func (transport *fakeAnswerTransport) DeliverMessage(_ context.Context, messageID string, _ string) error {
	transport.delivered = append(transport.delivered, messageID)
	return nil
}

func fakeAnswerRunner(
	publicKey ed25519.PublicKey,
	privateKey ed25519.PrivateKey,
	transport answerTransport,
	resolver repoStatusResolver,
) answerRunner {
	return answerRunner{
		options: answerOptions{
			agentID:        "workledger/agent",
			serverURL:      "http://hivebus.test",
			sessionID:      "wl-1",
			installationID: "install-1",
			project:        "hivebus",
			repoPath:       "/repo/hivebus",
			answerTTL:      defaultAnswerTTL,
			leaseDuration:  defaultAnswerLeaseDuration,
		},
		transport:  transport,
		resolver:   resolver,
		privateKey: privateKey,
		publicKey:  publicKey,
		random:     newCountingReader(),
		now:        fixedAnswerTime,
		answered:   make(map[string]struct{}),
	}
}

func signedAnswerTestQuery(
	t *testing.T,
	questionType string,
	publicKey ed25519.PublicKey,
	privateKey ed25519.PrivateKey,
) model.Envelope {
	t.Helper()

	payload, err := json.Marshal(askQueryPayload{
		Question:       "which checkout is canonical and what is HEAD?",
		QuestionType:   questionType,
		ReadOnly:       true,
		QueryPublicKey: base64.StdEncoding.EncodeToString(publicKey),
	})
	if err != nil {
		t.Fatalf("Marshal(query payload) error = %v", err)
	}

	return signAnswerTestQuery(t, questionType, payload, privateKey)
}

func signedAnswerTestQueryWithoutPayloadKey(
	t *testing.T,
	questionType string,
	privateKey ed25519.PrivateKey,
) model.Envelope {
	t.Helper()

	payload, err := json.Marshal(askQueryPayload{
		Question:     "which checkout is canonical and what is HEAD?",
		QuestionType: questionType,
		ReadOnly:     true,
	})
	if err != nil {
		t.Fatalf("Marshal(query payload) error = %v", err)
	}

	return signAnswerTestQuery(t, questionType, payload, privateKey)
}

func signAnswerTestQuery(
	t *testing.T,
	questionType string,
	payload []byte,
	privateKey ed25519.PrivateKey,
) model.Envelope {
	t.Helper()

	query := model.Envelope{
		MessageID:      "query-" + questionType,
		ThreadID:       "thread-answer-test",
		From:           "architect/session",
		To:             []string{"workledger/agent"},
		Type:           model.MessageTypeQuery,
		Payload:        payload,
		SentAt:         fixedAnswerTime(),
		IdempotencyKey: "idem-query-" + questionType,
		Trace: model.Trace{
			CorrelationID:   "corr-answer-test",
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed,
		},
		Security: model.Security{
			Scheme: model.SecuritySchemeEd25519,
			Nonce:  "nonce-query-" + questionType,
		},
	}
	signed, err := model.SignEnvelope(query, privateKey)
	if err != nil {
		t.Fatalf("SignEnvelope(query) error = %v", err)
	}

	return signed
}

func answerRecordForQuery(t *testing.T, query model.Envelope) answerAgentMessageRecord {
	t.Helper()

	body, err := json.Marshal(query)
	if err != nil {
		t.Fatalf("Marshal(query) error = %v", err)
	}

	return answerAgentMessageRecord{
		Message: answerAgentMessage{
			MessageID: query.MessageID,
			Body:      string(body),
		},
	}
}

func requireAnswerAuth(t *testing.T, request *http.Request, want string) {
	t.Helper()

	if got := request.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
}

func runGitTestCommand(t *testing.T, repoPath string, args ...string) string {
	t.Helper()

	commandArgs := append([]string(nil), args...)
	if repoPath != "" {
		commandArgs = append([]string{"-C", repoPath}, args...)
	}
	command := exec.Command("git", commandArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s error = %v\n%s", strings.Join(commandArgs, " "), err, strings.TrimSpace(string(output)))
	}

	return strings.TrimSpace(string(output))
}

func fixedAnswerTime() time.Time {
	return time.Date(2026, time.June, 6, 5, 0, 0, 0, time.UTC)
}
