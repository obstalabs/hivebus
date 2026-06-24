package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestBoardroomSayInboxRoundTripThroughRealRuntime(t *testing.T) {
	const (
		serverURL     = "http://hivebus.test"
		operatorToken = "operator-secret"
		workerToken   = "worker-secret"
		senderSession = "sess-codex"
		sender        = "codex/hivebus"
		targetSession = "sess-oracul"
		target        = "oracul/hivebus"
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
	sayCmd.SetIn(strings.NewReader("hello from codex without NR\n"))
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
	if got := inbox.Messages[0].Message.Body; got != "hello from codex without NR" {
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

type failingHTTPDoer struct {
	err error
}

func (doer failingHTTPDoer) Do(*http.Request) (*http.Response, error) {
	return nil, doer.err
}
