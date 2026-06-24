package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBoardroomSayInboxRoundTripThroughRealRuntime(t *testing.T) {
	const (
		serverURL     = "http://hivebus.test"
		operatorToken = "operator-secret"
		workerToken   = "worker-secret"
		senderSession = "sess-codex"
		sender        = "codex/hivebus"
		targetSession = "sess-peer"
		target        = "peer/hivebus"
		messageID     = "hbm-wo153"
	)

	client := handlerBackedClient(newRuntimeAskHandler(t))
	oldClient := boardroomHTTPClient
	boardroomHTTPClient = client
	t.Cleanup(func() { boardroomHTTPClient = oldClient })

	registerRuntimeAskSession(t, client, serverURL, workerToken, targetSession, target)

	sayCmd := newSayCommand()
	sayCmd.SetArgs([]string{
		"--server", serverURL,
		"--from", sender,
		"--to", target,
		"--session-id", senderSession,
		"--message-id", messageID,
		"--operator-token", operatorToken,
		"--worker-token", workerToken,
	})
	sayCmd.SetIn(strings.NewReader("hello from codex over boardroom\n"))
	var sayOut bytes.Buffer
	sayCmd.SetOut(&sayOut)
	if err := sayCmd.Execute(); err != nil {
		t.Fatalf("say command error = %v", err)
	}
	var sent boardroomSendOutput
	if err := json.Unmarshal(sayOut.Bytes(), &sent); err != nil {
		t.Fatalf("Unmarshal(say output) error = %v", err)
	}
	if sent.MessageID != messageID || sent.SenderParticipantID != sender || sent.TargetParticipantID != target {
		t.Fatalf("unexpected say output %#v", sent)
	}

	inboxCmd := newInboxCommand()
	inboxCmd.SetArgs([]string{
		"--server", serverURL,
		"--session-id", targetSession,
		"--worker-token", workerToken,
		"--ack",
	})
	var inboxOut bytes.Buffer
	inboxCmd.SetOut(&inboxOut)
	if err := inboxCmd.Execute(); err != nil {
		t.Fatalf("inbox command error = %v", err)
	}
	var inbox boardroomInboxOutput
	if err := json.Unmarshal(inboxOut.Bytes(), &inbox); err != nil {
		t.Fatalf("Unmarshal(inbox output) error = %v", err)
	}
	if len(inbox.Messages) != 1 {
		t.Fatalf("inbox messages = %d, want 1", len(inbox.Messages))
	}
	if got := inbox.Messages[0].Message.Body; got != "hello from codex over boardroom" {
		t.Fatalf("message body = %q", got)
	}
	if len(inbox.Delivered) != 1 || inbox.Delivered[0] != messageID {
		t.Fatalf("delivered = %#v, want %q", inbox.Delivered, messageID)
	}

	secondInbox := newInboxCommand()
	secondInbox.SetArgs([]string{
		"--server", serverURL,
		"--session-id", targetSession,
		"--worker-token", workerToken,
	})
	var secondOut bytes.Buffer
	secondInbox.SetOut(&secondOut)
	if err := secondInbox.Execute(); err != nil {
		t.Fatalf("second inbox command error = %v", err)
	}
	var empty boardroomInboxOutput
	if err := json.Unmarshal(secondOut.Bytes(), &empty); err != nil {
		t.Fatalf("Unmarshal(second inbox output) error = %v", err)
	}
	if len(empty.Messages) != 0 {
		t.Fatalf("second inbox messages = %#v, want none after ack", empty.Messages)
	}
}

func TestBoardroomListenAckReportsDeliveredIDs(t *testing.T) {
	const (
		serverURL     = "http://hivebus.test"
		operatorToken = "operator-secret"
		workerToken   = "worker-secret"
		senderSession = "sess-codex-listen"
		sender        = "codex/hivebus"
		targetSession = "sess-peer-listen"
		target        = "peer/hivebus"
		messageID     = "hbm-wo172"
	)

	client := handlerBackedClient(newRuntimeAskHandler(t))
	oldClient := boardroomHTTPClient
	boardroomHTTPClient = client
	t.Cleanup(func() { boardroomHTTPClient = oldClient })

	registerRuntimeAskSession(t, client, serverURL, workerToken, targetSession, target)

	sayCmd := newSayCommand()
	sayCmd.SetArgs([]string{
		"--server", serverURL,
		"--from", sender,
		"--to", target,
		"--session-id", senderSession,
		"--message-id", messageID,
		"--operator-token", operatorToken,
		"--worker-token", workerToken,
	})
	sayCmd.SetIn(strings.NewReader("ack this boardroom message\n"))
	var sayOut bytes.Buffer
	sayCmd.SetOut(&sayOut)
	if err := sayCmd.Execute(); err != nil {
		t.Fatalf("say command error = %v", err)
	}

	listenCmd := newListenCommand()
	listenCmd.SetArgs([]string{
		"--server", serverURL,
		"--session-id", targetSession,
		"--worker-token", workerToken,
		"--ack",
		"--timeout", "0s",
	})
	var listenOut bytes.Buffer
	listenCmd.SetOut(&listenOut)
	if err := listenCmd.Execute(); err != nil {
		t.Fatalf("listen command error = %v", err)
	}
	var listen boardroomListenOutput
	if err := json.Unmarshal(listenOut.Bytes(), &listen); err != nil {
		t.Fatalf("Unmarshal(listen output) error = %v", err)
	}
	if listen.Status != "message" || len(listen.Messages) != 1 {
		t.Fatalf("listen output = %#v, want one message", listen)
	}
	if len(listen.Delivered) != 1 || listen.Delivered[0] != messageID {
		t.Fatalf("listen delivered = %#v, want %q", listen.Delivered, messageID)
	}
}

func TestBoardroomListenTimeoutThroughRealRuntime(t *testing.T) {
	const (
		serverURL     = "http://hivebus.test"
		workerToken   = "worker-secret"
		targetSession = "sess-empty-listener"
		target        = "empty/listener"
	)

	client := handlerBackedClient(newRuntimeAskHandler(t))
	oldClient := boardroomHTTPClient
	boardroomHTTPClient = client
	t.Cleanup(func() { boardroomHTTPClient = oldClient })

	registerRuntimeAskSession(t, client, serverURL, workerToken, targetSession, target)

	listenCmd := newListenCommand()
	listenCmd.SetArgs([]string{
		"--server", serverURL,
		"--session-id", targetSession,
		"--worker-token", workerToken,
		"--timeout", "0s",
	})
	var output bytes.Buffer
	listenCmd.SetOut(&output)
	if err := listenCmd.Execute(); err != nil {
		t.Fatalf("listen timeout command error = %v", err)
	}
	var result boardroomListenOutput
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal(listen output) error = %v", err)
	}
	if result.Status != "timeout" {
		t.Fatalf("listen status = %q, want timeout", result.Status)
	}
}

func TestBoardroomListenReportsBusDown(t *testing.T) {
	oldClient := boardroomHTTPClient
	boardroomHTTPClient = failingHTTPDoer{err: errors.New("dial failed")}
	t.Cleanup(func() { boardroomHTTPClient = oldClient })

	listenCmd := newListenCommand()
	listenCmd.SetArgs([]string{
		"--server", "http://hivebus.test",
		"--session-id", "sess-bus-down",
		"--insecure",
		"--timeout", "0s",
	})
	var output bytes.Buffer
	listenCmd.SetOut(&output)
	err := listenCmd.Execute()
	if err == nil {
		t.Fatal("listen bus-down error = nil, want failure")
	}
	var result boardroomListenOutput
	if decodeErr := json.Unmarshal(output.Bytes(), &result); decodeErr != nil {
		t.Fatalf("Unmarshal(bus-down output) error = %v", decodeErr)
	}
	if result.Status != "bus_down" || !strings.Contains(result.Error, "dial failed") {
		t.Fatalf("bus-down output = %#v", result)
	}
}

func TestBoardroomTTLSecondsRoundsPositiveDurations(t *testing.T) {
	tests := map[string]struct {
		ttl  time.Duration
		want int
	}{
		"zero means omitted":        {ttl: 0, want: 0},
		"positive subsecond rounds": {ttl: 500 * time.Millisecond, want: 1},
		"whole second stays exact":  {ttl: 2 * time.Second, want: 2},
		"partial second rounds up":  {ttl: 2500 * time.Millisecond, want: 3},
		"negative remains no ttl":   {ttl: -time.Second, want: 0},
	}
	for name, tt := range tests {
		if got := boardroomTTLSeconds(tt.ttl); got != tt.want {
			t.Fatalf("%s: boardroomTTLSeconds(%s) = %d, want %d", name, tt.ttl, got, tt.want)
		}
	}
}

func TestBoardroomSayRejectsNegativeTTL(t *testing.T) {
	err := boardroomOptions{
		serverURL:     "http://hivebus.test",
		sessionID:     "sess-codex",
		from:          "codex/hivebus",
		to:            "peer/hivebus",
		operatorToken: "operator-secret",
		workerToken:   "worker-secret",
		leaseDuration: time.Hour,
		ttl:           -time.Nanosecond,
	}.validateSay()
	if err == nil || !strings.Contains(err.Error(), "ttl must be non-negative") {
		t.Fatalf("validateSay() error = %v, want negative ttl rejection", err)
	}
}

type failingHTTPDoer struct {
	err error
}

func (doer failingHTTPDoer) Do(*http.Request) (*http.Response, error) {
	return nil, doer.err
}
