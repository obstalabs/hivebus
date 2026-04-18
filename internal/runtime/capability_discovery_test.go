package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/store"
)

const testCapabilityValidationThreshold = 3

func TestCapabilityDiscoveryEndpointsAndTrustedRouting(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	keys := mustTestKeyStore(t)
	handler := NewHandler(st, openTestArtifactStore(t), keys)

	seedObservedCapability(t, st, "worker.smokevm", "go-testing", testCapabilityValidationThreshold)

	listReq := httptest.NewRequest(http.MethodGet, "/v0/workers/worker.smokevm/capabilities", nil)
	listReq.Header.Set("Authorization", "Bearer operator-secret")
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list capabilities status = %d, body = %s", listRec.Code, listRec.Body.String())
	}

	var listResp workerCapabilitiesResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("Unmarshal(listResp) error = %v", err)
	}
	if len(listResp.Capabilities) != 1 {
		t.Fatalf("expected one discovered capability, got %#v", listResp.Capabilities)
	}
	if listResp.Capabilities[0].TrustLevel != model.CapabilityTrustValidated ||
		!listResp.Capabilities[0].PendingApproval {
		t.Fatalf("expected validated pending capability, got %#v", listResp.Capabilities[0])
	}

	approveBody := marshalJSON(t, approveCapabilityRequest{ApprovedBy: "operator.root"})
	approveReq := httptest.NewRequest(
		http.MethodPost,
		"/v0/workers/worker.smokevm/capabilities/go-testing/approve",
		bytes.NewReader(approveBody),
	)
	approveReq.Header.Set("Content-Type", "application/json")
	approveReq.Header.Set("Authorization", "Bearer operator-secret")
	approveRec := httptest.NewRecorder()
	handler.ServeHTTP(approveRec, approveReq)
	if approveRec.Code != http.StatusOK {
		t.Fatalf("approve capability status = %d, body = %s", approveRec.Code, approveRec.Body.String())
	}

	thread := sampleThread()
	thread.ThreadID = "thr_capability_routing_http"
	thread.CreatedAt = time.Now().UTC()
	thread.UpdatedAt = thread.CreatedAt
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread(thread) error = %v", err)
	}

	task := sampleCapabilityTask(thread.ThreadID, "msg_capability_http", "idem_capability_http", "go-testing")
	task.SentAt = thread.CreatedAt.Add(time.Minute)
	if err := st.AppendEnvelope(t.Context(), task); err != nil {
		t.Fatalf("AppendEnvelope(task) error = %v", err)
	}

	pollBody := marshalJSON(t, workerPollRequest{WorkerID: "worker.smokevm"})
	pollReq := httptest.NewRequest(http.MethodPost, "/v0/workers/poll", bytes.NewReader(pollBody))
	pollReq.Header.Set("Content-Type", "application/json")
	pollReq.Header.Set("Authorization", "Bearer worker-secret")
	pollRec := httptest.NewRecorder()
	handler.ServeHTTP(pollRec, pollReq)
	if pollRec.Code != http.StatusOK {
		t.Fatalf("poll status = %d, body = %s", pollRec.Code, pollRec.Body.String())
	}

	var pollResp workerPollResponse
	if err := json.Unmarshal(pollRec.Body.Bytes(), &pollResp); err != nil {
		t.Fatalf("Unmarshal(pollResp) error = %v", err)
	}
	if pollResp.Status != string(store.PollResultAvailable) || pollResp.Task == nil ||
		pollResp.Task.Envelope.MessageID != task.MessageID {
		t.Fatalf("expected trusted capability task, got %#v", pollResp)
	}
}

func seedObservedCapability(
	t *testing.T,
	st *store.Store,
	workerID string,
	capabilityID string,
	count int,
) {
	t.Helper()

	baseTime := time.Date(2026, 4, 18, 11, 0, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		thread := sampleThread()
		thread.ThreadID = fmt.Sprintf("thr_capability_http_%d", i+1)
		thread.CreatedAt = baseTime.Add(time.Duration(i) * time.Hour)
		thread.UpdatedAt = thread.CreatedAt
		if _, err := st.AppendThread(t.Context(), thread); err != nil {
			t.Fatalf("AppendThread(%d) error = %v", i+1, err)
		}

		request := sampleWorkerTask(
			thread.ThreadID,
			fmt.Sprintf("msg_capability_task_%d", i+1),
			fmt.Sprintf("idem_capability_task_%d", i+1),
			workerID,
		)
		request.SentAt = thread.CreatedAt.Add(time.Minute)
		if err := st.AppendEnvelope(t.Context(), request); err != nil {
			t.Fatalf("AppendEnvelope(%d) error = %v", i+1, err)
		}

		accepted := sampleAcceptedEnvelope(
			request,
			workerID,
			fmt.Sprintf("msg_capability_accepted_%d", i+1),
			fmt.Sprintf("idem_capability_accepted_%d", i+1),
			request.SentAt.Add(time.Minute),
		)
		lease, err := st.ClaimTask(
			t.Context(),
			workerID,
			request.MessageID,
			accepted,
			5*time.Minute,
			request.SentAt.Add(time.Minute),
		)
		if err != nil {
			t.Fatalf("ClaimTask(%d) error = %v", i+1, err)
		}

		result := sampleResultEnvelopeWithTools(
			request,
			workerID,
			fmt.Sprintf("msg_capability_result_%d", i+1),
			fmt.Sprintf("idem_capability_result_%d", i+1),
			request.SentAt.Add(2*time.Minute),
			capabilityID,
		)
		if _, err := st.CompleteLease(
			t.Context(),
			lease.LeaseID,
			workerID,
			result,
			request.SentAt.Add(2*time.Minute),
		); err != nil {
			t.Fatalf("CompleteLease(%d) error = %v", i+1, err)
		}
	}
}
