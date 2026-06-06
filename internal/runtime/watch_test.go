package runtime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/store"
)

func TestWatchThreadStreamsOrderedEventsAndClosesOnFinal(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), nil)

	thread := sampleThread()
	mustCreateThread(t, handler, thread)

	task := sampleWorkerTask(thread.ThreadID, "msg_task", "idem_task", "worker.smokevm")
	mustAppendMessage(t, handler, thread.ThreadID, task, http.StatusAccepted)

	accepted := sampleAcceptedEnvelope(task, "worker.smokevm", "msg_accept", "idem_accept", task.SentAt.Add(1*time.Minute))
	claimBody := marshalJSON(t, workerClaimRequest{
		WorkerID:         "worker.smokevm",
		TaskMessageID:    task.MessageID,
		AcceptedEnvelope: accepted,
		LeaseSeconds:     300,
	})
	claimReq := httptest.NewRequest(http.MethodPost, "/v0/workers/claim", bytes.NewReader(claimBody))
	claimReq.Header.Set("Content-Type", "application/json")
	claimRec := httptest.NewRecorder()
	handler.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusAccepted {
		t.Fatalf("claim status = %d, body = %s", claimRec.Code, claimRec.Body.String())
	}

	var claimResp workerClaimResponse
	if err := json.Unmarshal(claimRec.Body.Bytes(), &claimResp); err != nil {
		t.Fatalf("Unmarshal(claim) error = %v", err)
	}

	partial := model.Envelope{
		MessageID:      "msg_partial",
		ThreadID:       thread.ThreadID,
		From:           "worker.smokevm",
		To:             []string{task.From},
		Type:           model.MessageTypeTaskResultPart,
		Payload:        []byte(`{"chunk":"running smoke"}`),
		ReplyTo:        task.MessageID,
		SentAt:         task.SentAt.Add(2 * time.Minute),
		IdempotencyKey: "idem_partial",
		Trace: model.Trace{
			CorrelationID: task.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_partial",
		},
	}
	partialBody := marshalJSON(t, leasePartialRequest{
		WorkerID:       "worker.smokevm",
		ResultEnvelope: partial,
	})
	partialReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/workers/leases/"+claimResp.Lease.LeaseID+"/partial",
		bytes.NewReader(partialBody),
	)
	partialReq.Header.Set("Content-Type", "application/json")
	partialRec := httptest.NewRecorder()
	handler.ServeHTTP(partialRec, partialReq)
	if partialRec.Code != http.StatusAccepted {
		t.Fatalf("partial status = %d, body = %s", partialRec.Code, partialRec.Body.String())
	}

	final := sampleResultEnvelope(task, "worker.smokevm", "msg_final", "idem_final", task.SentAt.Add(3*time.Minute))
	completeBody := marshalJSON(t, leaseCompleteRequest{
		WorkerID:       "worker.smokevm",
		ResultEnvelope: final,
	})
	completeReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/workers/leases/"+claimResp.Lease.LeaseID+"/complete",
		bytes.NewReader(completeBody),
	)
	completeReq.Header.Set("Content-Type", "application/json")
	completeRec := httptest.NewRecorder()
	handler.ServeHTTP(completeRec, completeReq)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body = %s", completeRec.Code, completeRec.Body.String())
	}

	events := mustCollectWatchEvents(t, handler, thread.ThreadID, "")
	if len(events) < 5 {
		t.Fatalf("expected at least 5 events, got %d", len(events))
	}
	if events[len(events)-2].Envelope == nil || events[len(events)-2].Envelope.Type != model.MessageTypeTaskResultPart {
		t.Fatalf("expected penultimate event to be partial result, got %#v", events[len(events)-2])
	}
	if events[len(events)-1].Envelope == nil || events[len(events)-1].Envelope.Type != model.MessageTypeTaskResultFinal {
		t.Fatalf("expected final event to close the stream, got %#v", events[len(events)-1])
	}
}

func TestWatchThreadResumesAfterLastEventID(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), nil)

	thread := sampleThread()
	mustCreateThread(t, handler, thread)

	task := sampleWorkerTask(thread.ThreadID, "msg_task", "idem_task", "worker.smokevm")
	mustAppendMessage(t, handler, thread.ThreadID, task, http.StatusAccepted)

	accepted := sampleAcceptedEnvelope(task, "worker.smokevm", "msg_accept", "idem_accept", task.SentAt.Add(1*time.Minute))
	claimBody := marshalJSON(t, workerClaimRequest{
		WorkerID:         "worker.smokevm",
		TaskMessageID:    task.MessageID,
		AcceptedEnvelope: accepted,
		LeaseSeconds:     300,
	})
	claimReq := httptest.NewRequest(http.MethodPost, "/v0/workers/claim", bytes.NewReader(claimBody))
	claimReq.Header.Set("Content-Type", "application/json")
	claimRec := httptest.NewRecorder()
	handler.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusAccepted {
		t.Fatalf("claim status = %d, body = %s", claimRec.Code, claimRec.Body.String())
	}

	var claimResp workerClaimResponse
	if err := json.Unmarshal(claimRec.Body.Bytes(), &claimResp); err != nil {
		t.Fatalf("Unmarshal(claim) error = %v", err)
	}

	partial := model.Envelope{
		MessageID:      "msg_partial",
		ThreadID:       thread.ThreadID,
		From:           "worker.smokevm",
		To:             []string{task.From},
		Type:           model.MessageTypeTaskResultPart,
		Payload:        []byte(`{"chunk":"running smoke"}`),
		ReplyTo:        task.MessageID,
		SentAt:         task.SentAt.Add(2 * time.Minute),
		IdempotencyKey: "idem_partial",
		Trace: model.Trace{
			CorrelationID: task.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_partial",
		},
	}
	if err := st.AppendLeaseResultPart(t.Context(), claimResp.Lease.LeaseID, "worker.smokevm", partial, partial.SentAt); err != nil {
		t.Fatalf("AppendLeaseResultPart() error = %v", err)
	}

	final := sampleResultEnvelope(task, "worker.smokevm", "msg_final", "idem_final", task.SentAt.Add(3*time.Minute))
	if _, err := st.CompleteLease(t.Context(), claimResp.Lease.LeaseID, "worker.smokevm", final, final.SentAt); err != nil {
		t.Fatalf("CompleteLease() error = %v", err)
	}

	allEvents, err := st.ListThreadEvents(t.Context(), thread.ThreadID, 0, 32)
	if err != nil {
		t.Fatalf("ListThreadEvents() error = %v", err)
	}
	if len(allEvents) < 2 {
		t.Fatalf("expected seeded events, got %d", len(allEvents))
	}
	var resumeFrom int64
	for _, event := range allEvents {
		if event.Envelope != nil && event.Envelope.Type == model.MessageTypeTaskResultPart {
			resumeFrom = event.Sequence
			break
		}
	}
	if resumeFrom == 0 {
		t.Fatal("expected to find partial-result sequence")
	}

	events := mustCollectWatchEvents(t, handler, thread.ThreadID, jsonNumberString(resumeFrom))
	if len(events) != 1 {
		t.Fatalf("expected 1 resumed event, got %d", len(events))
	}
	if events[0].Envelope == nil || events[0].Envelope.Type != model.MessageTypeTaskResultFinal {
		t.Fatalf("expected resumed final result, got %#v", events[0])
	}
}

func mustCollectWatchEvents(t *testing.T, handler http.Handler, threadID string, lastEventID string) []store.ThreadEvent {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/v0/threads/"+threadID+"/watch", nil)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	resp := rec.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("watch status = %d", resp.StatusCode)
	}

	var events []store.ThreadEvent
	if err := readSSEEvents(resp.Body, &events); err != nil {
		t.Fatalf("readSSEEvents() error = %v", err)
	}

	return events
}

func jsonNumberString(value int64) string {
	return strconv.FormatInt(value, 10)
}

func readSSEEvents(body io.Reader, target *[]store.ThreadEvent) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lines []string
	flush := func() error {
		if len(lines) == 0 {
			return nil
		}
		var event store.ThreadEvent
		if err := json.Unmarshal([]byte(strings.Join(lines, "\n")), &event); err != nil {
			return err
		}
		*target = append(*target, event)
		lines = lines[:0]
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	return flush()
}
