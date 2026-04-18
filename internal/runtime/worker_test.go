package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

func TestWorkerPollAndClaimOverHTTP(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), nil)

	thread := sampleThread()
	mustCreateThread(t, handler, thread)

	task := sampleWorkerTask(thread.ThreadID, "msg_task", "idem_task", "worker.smokevm")
	mustAppendMessage(t, handler, thread.ThreadID, task, http.StatusAccepted)

	pollBody := marshalJSON(t, workerPollRequest{
		WorkerID: "worker.smokevm",
	})
	pollReq := httptest.NewRequest(http.MethodPost, "/v0/workers/poll", bytes.NewReader(pollBody))
	pollReq.Header.Set("Content-Type", "application/json")
	pollRec := httptest.NewRecorder()
	handler.ServeHTTP(pollRec, pollReq)
	if pollRec.Code != http.StatusOK {
		t.Fatalf("POST /v0/workers/poll status = %d, body = %s", pollRec.Code, pollRec.Body.String())
	}

	var pollResponse workerPollResponse
	if err := json.Unmarshal(pollRec.Body.Bytes(), &pollResponse); err != nil {
		t.Fatalf("Unmarshal(poll) error = %v", err)
	}
	if pollResponse.Status != "available" || pollResponse.Task == nil {
		t.Fatalf("unexpected poll response %#v", pollResponse)
	}

	accepted := sampleAcceptedEnvelope(task, "worker.smokevm", "msg_accepted", "idem_accepted", task.SentAt.Add(1*time.Minute))
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
		t.Fatalf("POST /v0/workers/claim status = %d, body = %s", claimRec.Code, claimRec.Body.String())
	}

	var claimResponse workerClaimResponse
	if err := json.Unmarshal(claimRec.Body.Bytes(), &claimResponse); err != nil {
		t.Fatalf("Unmarshal(claim) error = %v", err)
	}
	if claimResponse.Status != "claimed" {
		t.Fatalf("unexpected claim response %#v", claimResponse)
	}
}

func TestWorkerClaimRejectsSecondActiveLease(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), nil)

	thread := sampleThread()
	mustCreateThread(t, handler, thread)

	firstTask := sampleWorkerTask(thread.ThreadID, "msg_task_1", "idem_task_1", "worker.smokevm")
	secondTask := sampleWorkerTask(thread.ThreadID, "msg_task_2", "idem_task_2", "worker.smokevm")
	secondTask.SentAt = secondTask.SentAt.Add(1 * time.Minute)
	mustAppendMessage(t, handler, thread.ThreadID, firstTask, http.StatusAccepted)
	mustAppendMessage(t, handler, thread.ThreadID, secondTask, http.StatusAccepted)

	firstAccepted := sampleAcceptedEnvelope(firstTask, "worker.smokevm", "msg_accepted_1", "idem_accepted_1", firstTask.SentAt.Add(1*time.Minute))
	firstClaimBody := marshalJSON(t, workerClaimRequest{
		WorkerID:         "worker.smokevm",
		TaskMessageID:    firstTask.MessageID,
		AcceptedEnvelope: firstAccepted,
		LeaseSeconds:     300,
	})
	firstClaimReq := httptest.NewRequest(http.MethodPost, "/v0/workers/claim", bytes.NewReader(firstClaimBody))
	firstClaimReq.Header.Set("Content-Type", "application/json")
	firstClaimRec := httptest.NewRecorder()
	handler.ServeHTTP(firstClaimRec, firstClaimReq)
	if firstClaimRec.Code != http.StatusAccepted {
		t.Fatalf("first claim status = %d, body = %s", firstClaimRec.Code, firstClaimRec.Body.String())
	}

	secondAccepted := sampleAcceptedEnvelope(secondTask, "worker.smokevm", "msg_accepted_2", "idem_accepted_2", secondTask.SentAt.Add(1*time.Minute))
	secondClaimBody := marshalJSON(t, workerClaimRequest{
		WorkerID:         "worker.smokevm",
		TaskMessageID:    secondTask.MessageID,
		AcceptedEnvelope: secondAccepted,
		LeaseSeconds:     300,
	})
	secondClaimReq := httptest.NewRequest(http.MethodPost, "/v0/workers/claim", bytes.NewReader(secondClaimBody))
	secondClaimReq.Header.Set("Content-Type", "application/json")
	secondClaimRec := httptest.NewRecorder()
	handler.ServeHTTP(secondClaimRec, secondClaimReq)
	if secondClaimRec.Code != http.StatusConflict {
		t.Fatalf("second claim status = %d, body = %s", secondClaimRec.Code, secondClaimRec.Body.String())
	}
}

func mustCreateThread(t *testing.T, handler http.Handler, thread any) {
	t.Helper()

	body := marshalJSON(t, thread)
	req := httptest.NewRequest(http.MethodPost, "/v0/threads", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v0/threads status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func mustAppendMessage(t *testing.T, handler http.Handler, threadID string, envelope any, expectedStatus int) {
	t.Helper()

	body := marshalJSON(t, envelope)
	req := httptest.NewRequest(http.MethodPost, "/v0/threads/"+threadID+"/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != expectedStatus {
		t.Fatalf("POST /v0/threads/%s/messages status = %d, body = %s", threadID, rec.Code, rec.Body.String())
	}
}

func marshalJSON(t *testing.T, value any) []byte {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	return body
}

func sampleAcceptedEnvelope(
	request model.Envelope,
	workerID string,
	messageID string,
	idempotencyKey string,
	sentAt time.Time,
) model.Envelope {
	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       request.ThreadID,
		From:           workerID,
		To:             []string{request.From},
		Type:           model.MessageTypeTaskAccepted,
		Payload:        json.RawMessage(`{"status":"accepted"}`),
		ReplyTo:        request.MessageID,
		SentAt:         sentAt,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + messageID,
		},
	}
}

func sampleResultEnvelope(
	request model.Envelope,
	workerID string,
	messageID string,
	idempotencyKey string,
	sentAt time.Time,
) model.Envelope {
	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       request.ThreadID,
		From:           workerID,
		To:             []string{request.From},
		Type:           model.MessageTypeTaskResultFinal,
		Payload:        json.RawMessage(`{"status":"done"}`),
		ReplyTo:        request.MessageID,
		SentAt:         sentAt,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + messageID,
		},
	}
}

func sampleResultEnvelopeWithTools(
	request model.Envelope,
	workerID string,
	messageID string,
	idempotencyKey string,
	sentAt time.Time,
	toolsUsed ...string,
) model.Envelope {
	payload, err := json.Marshal(model.TaskResultFinalPayload{
		Status:    "done",
		ToolsUsed: toolsUsed,
	})
	if err != nil {
		panic(err)
	}

	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       request.ThreadID,
		From:           workerID,
		To:             []string{request.From},
		Type:           model.MessageTypeTaskResultFinal,
		Payload:        payload,
		ReplyTo:        request.MessageID,
		SentAt:         sentAt,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID: request.Trace.CorrelationID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + messageID,
		},
	}
}

func sampleWorkerTask(
	threadID string,
	messageID string,
	idempotencyKey string,
	workerID string,
) model.Envelope {
	now := time.Date(2026, 4, 15, 7, 1, 0, 0, time.UTC)

	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       threadID,
		From:           "collector.nullbot",
		To:             []string{workerID},
		Type:           model.MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"smoke lane unstable"}`),
		SentAt:         now,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID: "corr_" + messageID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + messageID,
		},
	}
}

func sampleCapabilityTask(
	threadID string,
	messageID string,
	idempotencyKey string,
	capability string,
) model.Envelope {
	now := time.Date(2026, 4, 15, 6, 1, 0, 0, time.UTC)

	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       threadID,
		From:           "collector.nullbot",
		Capability:     capability,
		Type:           model.MessageTypeTaskRequest,
		Payload:        json.RawMessage(`{"issue":"smoke vm context lost"}`),
		SentAt:         now,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID: "corr_" + messageID,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + messageID,
		},
	}
}
