package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	"github.com/ppiankov/hivebus/internal/store"
	"github.com/ppiankov/hivebus/internal/work"
)

const (
	workledgerParticipantID = "service.workledger"
	hivebusParticipantID    = "service.hivebus"
)

type promoteThreadRequest struct {
	RequestedBy         string          `json:"requested_by"`
	WorkledgerProject   string          `json:"workledger_project"`
	Diagnosis           model.Diagnosis `json:"diagnosis"`
	OptionalSyncTargets []string        `json:"optional_sync_targets,omitempty"`
}

type promoteThreadResponse struct {
	Status    string               `json:"status"`
	WorkOrder work.Draft           `json:"work_order"`
	Canonical WorkOrderRef         `json:"canonical"`
	Sync      []SyncReceipt        `json:"sync,omitempty"`
	Snapshot  store.ThreadSnapshot `json:"snapshot"`
}

func (s *server) handlePromoteThread(w http.ResponseWriter, r *http.Request) {
	if s.workOrders == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("workledger bridge is not configured"))
		return
	}

	var request promoteThreadRequest
	if err := decodeJSON(r.Body, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validatePromoteThreadRequest(request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	threadID := r.PathValue("threadID")
	snapshot, err := s.store.LoadThread(r.Context(), threadID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrThreadNotFound):
			writeError(w, http.StatusNotFound, err)
		default:
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}

	now := currentTime()
	if snapshot.Thread.Status != model.ThreadStatusReadyForWork &&
		snapshot.Thread.Status != model.ThreadStatusDone {
		snapshot, err = s.store.TransitionThread(r.Context(), threadID, model.ThreadStatusReadyForWork, now)
		if err != nil {
			if isInputError(err) {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}

	diagnosisEnvelope, err := buildDiagnosisEnvelope(threadID, request, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := appendEnvelopeIfMissing(r.Context(), s.store, diagnosisEnvelope); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	draft, err := work.DraftFromThread(request.WorkledgerProject, snapshot.Thread, request.Diagnosis)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(request.OptionalSyncTargets) > 0 {
		draft.OptionalSyncTargets = append([]string(nil), request.OptionalSyncTargets...)
	}

	canonical, err := s.workOrders.CreateWorkOrder(r.Context(), draft)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	workOrderEnvelope, err := buildWorkOrderEnvelope(threadID, request, draft, canonical, now.Add(1*time.Second))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := appendEnvelopeIfMissing(r.Context(), s.store, workOrderEnvelope); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	syncReceipts := s.runSyncHooks(r.Context(), draft, canonical, request.OptionalSyncTargets)
	snapshot, err = s.store.LoadThread(r.Context(), threadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusCreated, promoteThreadResponse{
		Status:    "promoted",
		WorkOrder: draft,
		Canonical: canonical,
		Sync:      syncReceipts,
		Snapshot:  snapshot,
	})
}

func validatePromoteThreadRequest(request promoteThreadRequest) error {
	if strings.TrimSpace(request.RequestedBy) == "" {
		return errors.New("requested_by is required")
	}
	if strings.TrimSpace(request.WorkledgerProject) == "" {
		return errors.New("workledger_project is required")
	}
	if err := request.Diagnosis.Validate(); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(request.OptionalSyncTargets))
	for _, target := range request.OptionalSyncTargets {
		target = strings.TrimSpace(target)
		if target == "" {
			return errors.New("optional_sync_targets contains an empty target")
		}
		if _, ok := seen[target]; ok {
			return fmt.Errorf("duplicate optional sync target %q", target)
		}
		seen[target] = struct{}{}
	}

	return nil
}

func buildDiagnosisEnvelope(
	threadID string,
	request promoteThreadRequest,
	at time.Time,
) (model.Envelope, error) {
	payload, err := json.Marshal(request.Diagnosis)
	if err != nil {
		return model.Envelope{}, fmt.Errorf("marshal diagnosis payload: %w", err)
	}

	digest := stableDigest(threadID, request.WorkledgerProject, request.Diagnosis.Problem)
	return model.Envelope{
		MessageID:      "msg_diagnosis_" + digest,
		ThreadID:       threadID,
		From:           request.RequestedBy,
		To:             []string{hivebusParticipantID},
		Type:           model.MessageTypeDiagnosisPropose,
		Payload:        payload,
		SentAt:         at,
		IdempotencyKey: "idem_diagnosis_" + digest,
		Trace: model.Trace{
			CorrelationID: threadID,
			SpanID:        "promote.diagnosis",
			Model:         "hivebus",
			Verified:      true,
		},
		Security: model.Security{
			Scheme:    "ed25519",
			Nonce:     "nonce_diagnosis_" + digest,
			Signature: "sig_diagnosis_" + digest,
			Signed:    true,
		},
	}, nil
}

func buildWorkOrderEnvelope(
	threadID string,
	request promoteThreadRequest,
	draft work.Draft,
	canonical WorkOrderRef,
	at time.Time,
) (model.Envelope, error) {
	payload, err := json.Marshal(map[string]any{
		"tracking_system":       draft.TrackingSystem,
		"workledger_project":    draft.WorkledgerProject,
		"work_order_id":         canonical.ID,
		"work_order_title":      canonical.Title,
		"source_thread_id":      draft.SourceThreadID,
		"optional_sync_targets": request.OptionalSyncTargets,
		"confidence":            draft.Confidence,
		"evidence_ids":          draft.EvidenceIDs,
	})
	if err != nil {
		return model.Envelope{}, fmt.Errorf("marshal work order payload: %w", err)
	}

	digest := stableDigest(threadID, draft.WorkledgerProject, canonical.Title)
	return model.Envelope{
		MessageID:      "msg_work_order_" + digest,
		ThreadID:       threadID,
		From:           hivebusParticipantID,
		To:             []string{workledgerParticipantID},
		Type:           model.MessageTypeWorkOrderCreate,
		Payload:        payload,
		SentAt:         at,
		IdempotencyKey: "idem_work_order_" + digest,
		Trace: model.Trace{
			CorrelationID: threadID,
			SpanID:        "promote.work_order",
			Model:         "hivebus",
			Verified:      true,
		},
		Security: model.Security{
			Scheme:    "ed25519",
			Nonce:     "nonce_work_order_" + digest,
			Signature: "sig_work_order_" + digest,
			Signed:    true,
		},
	}, nil
}

func appendEnvelopeIfMissing(ctx context.Context, st *store.Store, envelope model.Envelope) error {
	err := st.AppendEnvelope(ctx, envelope)
	if errors.Is(err, store.ErrDuplicateMessage) || errors.Is(err, store.ErrDuplicateIdempotencyKey) {
		return nil
	}
	return err
}

func (s *server) runSyncHooks(
	ctx context.Context,
	draft work.Draft,
	canonical WorkOrderRef,
	targets []string,
) []SyncReceipt {
	if len(targets) == 0 {
		return nil
	}

	receipts := make([]SyncReceipt, 0, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(target)
		hook, ok := s.syncHooks[target]
		if !ok {
			receipts = append(receipts, SyncReceipt{
				Target: target,
				Status: "not_configured",
				Detail: "no sync hook configured",
			})
			continue
		}

		receipt, err := hook.SyncWorkOrder(ctx, draft, canonical)
		if err != nil {
			receipts = append(receipts, SyncReceipt{
				Target: target,
				Status: "failed",
				Detail: err.Error(),
			})
			continue
		}
		if receipt.Target == "" {
			receipt.Target = target
		}
		receipts = append(receipts, receipt)
	}

	return receipts
}

func stableDigest(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(hash[:8])
}
