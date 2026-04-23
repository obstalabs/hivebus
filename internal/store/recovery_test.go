package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

func TestBuildPromotedThreadRecoveryCapsuleUsesVerifiedProvenanceOnly(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	artifact := model.Artifact{
		ArtifactID:  "art_smoke_log",
		Name:        "smoke.log",
		Kind:        "log",
		URI:         "artifact://sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SHA256:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SizeBytes:   512,
		ContentType: "text/plain",
		Redacted:    true,
	}
	if err := st.AppendArtifact(t.Context(), thread.ThreadID, artifact, formatTime(thread.CreatedAt.Add(time.Minute))); err != nil {
		t.Fatalf("AppendArtifact() error = %v", err)
	}

	appendTestEnvelope(t, st, model.Envelope{
		MessageID:      "msg_stale_fact",
		ThreadID:       thread.ThreadID,
		From:           "agent.investigator",
		To:             []string{"service.hivebus"},
		Type:           model.MessageTypeTaskResultPart,
		Payload:        json.RawMessage(`{"recovery_facts":[{"state":"rejected","text":"ssh host was the old disposable VM"}]}`),
		SentAt:         thread.CreatedAt.Add(2 * time.Minute),
		IdempotencyKey: "idem_stale_fact",
		Trace: model.Trace{
			CorrelationID: thread.ThreadID,
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_stale_fact",
		},
	})
	appendTestEnvelope(t, st, model.Envelope{
		MessageID:      "msg_unlabeled_history",
		ThreadID:       thread.ThreadID,
		From:           "agent.investigator",
		To:             []string{"service.hivebus"},
		Type:           model.MessageTypeTaskResultPart,
		Payload:        json.RawMessage(`{"hypothesis":"raw unlabeled hypothesis should not be replayed"}`),
		SentAt:         thread.CreatedAt.Add(3 * time.Minute),
		IdempotencyKey: "idem_unlabeled_history",
		Trace: model.Trace{
			CorrelationID: thread.ThreadID,
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_unlabeled_history",
		},
	})

	diagnosis := model.Diagnosis{
		Problem:             "Codex loses smoke VM context after compaction.",
		LikelyCause:         "Only raw thread history preserved the VM handoff, so the current task drifted after compaction.",
		ProposedRemediation: []string{"Publish a verified recovery capsule with explicit stale-fact labels."},
		EvidenceIDs:         []string{artifact.ArtifactID},
		Confidence:          model.ConfidenceHigh,
		Verified:            true,
	}
	appendTestEnvelope(t, st, diagnosisEnvelope(t, thread.ThreadID, diagnosis, thread.CreatedAt.Add(4*time.Minute)))
	appendTestEnvelope(t, st, workOrderEnvelope(thread.ThreadID, thread.CreatedAt.Add(5*time.Minute)))

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	capsule, err := BuildPromotedThreadRecoveryCapsule(
		snapshot,
		time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("BuildPromotedThreadRecoveryCapsule() error = %v", err)
	}

	if capsule.Type != model.RecoveryCapsuleTypePromotedThread {
		t.Fatalf("unexpected capsule type %q", capsule.Type)
	}
	if !capsule.VerifiedDiagnosis.Verified {
		t.Fatal("expected verified diagnosis summary")
	}
	if capsule.Promotion.WorkOrderID != 44 {
		t.Fatalf("expected promotion WO-44, got %#v", capsule.Promotion)
	}
	if len(capsule.Evidence) != 1 || capsule.Evidence[0].ArtifactID != artifact.ArtifactID {
		t.Fatalf("expected evidence ref for %q, got %#v", artifact.ArtifactID, capsule.Evidence)
	}
	if len(capsule.RejectedOrStale) != 1 {
		t.Fatalf("expected one rejected fact, got %#v", capsule.RejectedOrStale)
	}
	if capsule.RejectedOrStale[0].State != model.RecoveryFactRejected {
		t.Fatalf("expected rejected fact label, got %#v", capsule.RejectedOrStale[0])
	}
	if capsule.MissingInfoResolution.Status != "resolved" {
		t.Fatalf("expected resolved missing info, got %#v", capsule.MissingInfoResolution)
	}

	body := string(marshalJSONForTest(t, capsule))
	if strings.Contains(body, "raw unlabeled hypothesis") {
		t.Fatalf("capsule leaked unlabeled raw history: %s", body)
	}
	if strings.Contains(body, artifact.URI) {
		t.Fatalf("capsule leaked artifact URI instead of compact ref: %s", body)
	}
}

func TestBuildPromotedThreadRecoveryCapsuleRequiresPromotionAndResolvedDiagnosis(t *testing.T) {
	t.Helper()

	st := openTestStore(t)
	thread := sampleThread()
	thread.Status = model.ThreadStatusReadyForWork
	if _, err := st.AppendThread(t.Context(), thread); err != nil {
		t.Fatalf("AppendThread() error = %v", err)
	}

	diagnosis := model.Diagnosis{
		Problem:             "Smoke lane blocked.",
		LikelyCause:         "Missing VM context.",
		ProposedRemediation: []string{"Ask operator for current VM."},
		MissingInfo:         []string{"current VM address"},
		EvidenceIDs:         []string{"art_missing"},
		Confidence:          model.ConfidenceMedium,
		Verified:            true,
	}
	appendTestEnvelope(t, st, diagnosisEnvelope(t, thread.ThreadID, diagnosis, thread.CreatedAt.Add(time.Minute)))

	snapshot, err := st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread() error = %v", err)
	}
	if _, err := BuildPromotedThreadRecoveryCapsule(snapshot, thread.CreatedAt); err == nil {
		t.Fatal("expected unresolved missing_info to block recovery capsule")
	}

	diagnosis.MissingInfo = nil
	appendTestEnvelope(t, st, diagnosisEnvelopeWithID(
		t,
		thread.ThreadID,
		diagnosis,
		"msg_diagnosis_resolved",
		"idem_diagnosis_resolved",
		thread.CreatedAt.Add(2*time.Minute),
	))
	snapshot, err = st.LoadThread(t.Context(), thread.ThreadID)
	if err != nil {
		t.Fatalf("LoadThread(resolved) error = %v", err)
	}
	if _, err := BuildPromotedThreadRecoveryCapsule(snapshot, thread.CreatedAt); err == nil {
		t.Fatal("expected missing promotion receipt to block recovery capsule")
	}
}

func appendTestEnvelope(t *testing.T, st *Store, envelope model.Envelope) {
	t.Helper()

	if err := st.AppendEnvelope(t.Context(), envelope); err != nil {
		t.Fatalf("AppendEnvelope(%s) error = %v", envelope.MessageID, err)
	}
}

func diagnosisEnvelope(
	t *testing.T,
	threadID string,
	diagnosis model.Diagnosis,
	at time.Time,
) model.Envelope {
	t.Helper()

	return diagnosisEnvelopeWithID(t, threadID, diagnosis, "msg_diagnosis", "idem_diagnosis", at)
}

func diagnosisEnvelopeWithID(
	t *testing.T,
	threadID string,
	diagnosis model.Diagnosis,
	messageID string,
	idempotencyKey string,
	at time.Time,
) model.Envelope {
	t.Helper()

	payload := marshalJSONForTest(t, diagnosis)
	return model.Envelope{
		MessageID:      messageID,
		ThreadID:       threadID,
		From:           "agent.investigator",
		To:             []string{"service.hivebus"},
		Type:           model.MessageTypeDiagnosisPropose,
		Payload:        payload,
		SentAt:         at,
		IdempotencyKey: idempotencyKey,
		Trace: model.Trace{
			CorrelationID: threadID,
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_" + messageID,
		},
	}
}

func workOrderEnvelope(threadID string, at time.Time) model.Envelope {
	return model.Envelope{
		MessageID: "msg_work_order",
		ThreadID:  threadID,
		From:      "service.hivebus",
		To:        []string{"service.workledger"},
		Type:      model.MessageTypeWorkOrderCreate,
		Payload: json.RawMessage(`{
			"tracking_system":"workledger",
			"workledger_project":"hivebus",
			"work_order_id":44,
			"work_order_title":"Promoted thread recovery capsule",
			"source_thread_id":"` + threadID + `",
			"optional_sync_targets":["hiveram.com"],
			"confidence":"high",
			"evidence_ids":["art_smoke_log"]
		}`),
		SentAt:         at,
		IdempotencyKey: "idem_work_order",
		Trace: model.Trace{
			CorrelationID: threadID,
			Verified:      true,
		},
		Security: model.Security{
			Scheme: "ed25519",
			Nonce:  "nonce_work_order",
		},
	}
}

func marshalJSONForTest(t *testing.T, value any) json.RawMessage {
	t.Helper()

	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return payload
}
