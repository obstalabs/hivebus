package spec

import (
	"encoding/json"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/work"
)

// CaseBundle demonstrates the intended nullbot -> agents -> workledger flow.
type CaseBundle struct {
	Thread    model.Thread     `json:"thread"`
	Messages  []model.Envelope `json:"messages"`
	Diagnosis model.Diagnosis  `json:"diagnosis"`
	WorkOrder work.Draft       `json:"work_order"`
}

// SampleCase returns a fully verified sample ready for WO creation.
func SampleCase() CaseBundle {
	createdAt := time.Date(2026, 3, 31, 8, 0, 0, 0, time.UTC)
	readyAt := createdAt.Add(45 * time.Minute)
	deadline := createdAt.Add(2 * time.Hour)

	thread := model.Thread{
		ThreadID:     "thr_prod_api_502",
		Title:        "API gateway returns 502 during invoice sync",
		Status:       model.ThreadStatusReadyForWork,
		CustomerTier: model.TierPro,
		Source:       "nullbot",
		Summary:      "Nullbot collected gateway errors, Postgres saturation, and deployment metadata.",
		Participants: []model.Participant{
			{ID: "collector.nullbot", Kind: model.ParticipantCollector},
			{ID: "agent.dispatch", Kind: model.ParticipantService, Capabilities: []string{"route.case"}},
			{ID: "agent.investigator", Kind: model.ParticipantAgent, Capabilities: []string{"incident.diagnose"}},
			{ID: "service.workledger", Kind: model.ParticipantService, Capabilities: []string{"work_order.create"}},
			{ID: "service.hiveram", Kind: model.ParticipantService, Capabilities: []string{"commercial.sync"}},
		},
		Evidence: []model.Artifact{
			{
				ArtifactID: "art_gateway_logs",
				Name:       "gateway-errors.log",
				Kind:       "log",
				URI:        "s3://hivebus-demo/artifacts/gateway-errors.log",
				SHA256:     "8c3e73b2",
				SizeBytes:  2048,
			},
			{
				ArtifactID: "art_pg_stat",
				Name:       "pg-stat-activity.json",
				Kind:       "metric",
				URI:        "s3://hivebus-demo/artifacts/pg-stat-activity.json",
				SHA256:     "14d771a1",
				SizeBytes:  1024,
			},
		},
		CreatedAt: createdAt,
		UpdatedAt: readyAt,
	}

	taskPayload, err := json.Marshal(map[string]any{
		"issue":       "API gateway returns 502 during invoice sync",
		"environment": "prod-eu-1",
		"bundle_id":   "bundle_20260331_0800",
	})
	if err != nil {
		panic(err)
	}

	questionPayload, err := json.Marshal(map[string]string{
		"question": "Did the incident start after the 07:30 deployment?",
	})
	if err != nil {
		panic(err)
	}

	answerPayload, err := json.Marshal(map[string]string{
		"answer": "Yes. The error rate increased within three minutes of deployment.",
	})
	if err != nil {
		panic(err)
	}

	diagnosisPayload, err := json.Marshal(map[string]string{
		"cause": "API worker fan-out exceeded the available Postgres pool after deployment.",
	})
	if err != nil {
		panic(err)
	}

	messages := []model.Envelope{
		{
			MessageID:      "msg_001",
			ThreadID:       thread.ThreadID,
			From:           "collector.nullbot",
			To:             []string{"agent.dispatch"},
			Type:           model.MessageTypeTaskRequest,
			Capability:     "incident.diagnose",
			Payload:        taskPayload,
			ArtifactIDs:    []string{"art_gateway_logs", "art_pg_stat"},
			Deadline:       &deadline,
			SentAt:         createdAt,
			IdempotencyKey: "idem_thr_prod_api_502_msg_001",
			Trace: model.Trace{
				CorrelationID: "corr_thr_prod_api_502",
				SpanID:        "span_collect",
				Model:         "nullbot-local",
				Verified:      true,
			},
			Security: model.Security{
				Scheme:    "ed25519",
				Nonce:     "nonce_msg_001",
				Signature: "sig_msg_001",
				Signed:    true,
			},
		},
		{
			MessageID:      "msg_002",
			ThreadID:       thread.ThreadID,
			From:           "agent.investigator",
			To:             []string{"collector.nullbot"},
			Type:           model.MessageTypeClarifyRequest,
			Payload:        questionPayload,
			SentAt:         createdAt.Add(10 * time.Minute),
			IdempotencyKey: "idem_thr_prod_api_502_msg_002",
			Trace: model.Trace{
				CorrelationID: "corr_thr_prod_api_502",
				SpanID:        "span_question",
				Model:         "gpt-6-investigator",
				Verified:      true,
			},
			Security: model.Security{
				Scheme:    "ed25519",
				Nonce:     "nonce_msg_002",
				Signature: "sig_msg_002",
				Signed:    true,
			},
		},
		{
			MessageID:      "msg_003",
			ThreadID:       thread.ThreadID,
			From:           "collector.nullbot",
			To:             []string{"agent.investigator"},
			Type:           model.MessageTypeClarifyResponse,
			Payload:        answerPayload,
			ReplyTo:        "msg_002",
			SentAt:         createdAt.Add(15 * time.Minute),
			IdempotencyKey: "idem_thr_prod_api_502_msg_003",
			Trace: model.Trace{
				CorrelationID: "corr_thr_prod_api_502",
				SpanID:        "span_answer",
				Model:         "nullbot-local",
				Verified:      true,
			},
			Security: model.Security{
				Scheme:    "ed25519",
				Nonce:     "nonce_msg_003",
				Signature: "sig_msg_003",
				Signed:    true,
			},
		},
		{
			MessageID:      "msg_004",
			ThreadID:       thread.ThreadID,
			From:           "agent.investigator",
			To:             []string{"service.workledger"},
			Type:           model.MessageTypeDiagnosisPropose,
			Payload:        diagnosisPayload,
			ArtifactIDs:    []string{"art_gateway_logs", "art_pg_stat"},
			SentAt:         readyAt,
			IdempotencyKey: "idem_thr_prod_api_502_msg_004",
			Trace: model.Trace{
				CorrelationID: "corr_thr_prod_api_502",
				SpanID:        "span_diagnosis",
				Model:         "gpt-6-investigator",
				Verified:      true,
			},
			Security: model.Security{
				Scheme:    "ed25519",
				Nonce:     "nonce_msg_004",
				Signature: "sig_msg_004",
				Signed:    true,
			},
		},
	}

	diagnosis := model.Diagnosis{
		Problem:     "API gateway returns 502 during invoice sync because upstream requests exhaust the Postgres connection pool.",
		LikelyCause: "The 07:30 deployment increased API worker fan-out without raising the database pool limit.",
		ProposedRemediation: []string{
			"Reduce API worker fan-out to match the available Postgres pool.",
			"Raise the Postgres pool ceiling for the production environment.",
			"Re-run the invoice sync after deploying the safer worker profile.",
		},
		EvidenceIDs: []string{"art_gateway_logs", "art_pg_stat"},
		Confidence:  model.ConfidenceHigh,
		Verified:    true,
	}

	workOrder, err := work.DraftFromThread(thread, diagnosis)
	if err != nil {
		panic(err)
	}

	workOrder.OptionalSyncTargets = []string{"hiveram.com"}

	return CaseBundle{
		Thread:    thread,
		Messages:  messages,
		Diagnosis: diagnosis,
		WorkOrder: workOrder,
	}
}
