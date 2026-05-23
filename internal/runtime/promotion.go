package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
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
	pendingRecord := buildPendingPromotionRecord(diagnosisEnvelope, now)

	draft, err := work.DraftFromThread(request.WorkledgerProject, snapshot.Thread, request.Diagnosis)
	if err != nil {
		if recordErr := recordFailedPendingPromotion(r.Context(), s.store, pendingRecord, err, currentTime()); recordErr != nil {
			writeError(w, http.StatusInternalServerError, recordErr)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(request.OptionalSyncTargets) > 0 {
		draft.OptionalSyncTargets = append([]string(nil), request.OptionalSyncTargets...)
	}

	if canonical, ok := existingPromotedRetry(snapshot, diagnosisEnvelope, draft); ok {
		writeJSON(w, http.StatusOK, promoteThreadResponse{
			Status:    "duplicate",
			WorkOrder: draft,
			Canonical: canonical,
			Snapshot:  snapshot,
		})
		return
	}

	if err := s.store.RecordPromotionPending(r.Context(), pendingRecord); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	canonical, err := s.workOrders.CreateWorkOrder(r.Context(), draft)
	if err != nil {
		if recordErr := recordFailedPendingPromotion(r.Context(), s.store, pendingRecord, err, currentTime()); recordErr != nil {
			writeError(w, http.StatusInternalServerError, recordErr)
			return
		}
		writeError(w, http.StatusBadGateway, err)
		return
	}

	workOrderEnvelope, err := buildWorkOrderEnvelope(threadID, request, draft, canonical, now.Add(1*time.Second))
	if err != nil {
		if recordErr := recordFailedPendingPromotion(r.Context(), s.store, pendingRecord, err, currentTime()); recordErr != nil {
			writeError(w, http.StatusInternalServerError, recordErr)
			return
		}
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.FinalizePromotion(
		r.Context(),
		pendingRecord.PendingMessageID,
		now.Add(2*time.Second),
		diagnosisEnvelope,
		workOrderEnvelope,
	); err != nil {
		if recordErr := recordFailedPendingPromotion(r.Context(), s.store, pendingRecord, err, currentTime()); recordErr != nil {
			writeError(w, http.StatusInternalServerError, recordErr)
			return
		}
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

// promotedRetryReceipt mirrors the receipt fields needed for WO-72 exact
// retry binding without widening the public promotion response schema.
type promotedRetryReceipt struct {
	TrackingSystem         string           `json:"tracking_system"`
	WorkledgerProject      string           `json:"workledger_project"`
	WorkOrderID            int              `json:"work_order_id"`
	WorkOrderTitle         string           `json:"work_order_title"`
	SourceThreadID         string           `json:"source_thread_id"`
	OptionalSyncTargets    []string         `json:"optional_sync_targets"`
	Confidence             model.Confidence `json:"confidence"`
	EvidenceIDs            []string         `json:"evidence_ids"`
	DiagnosisMessageID     string           `json:"diagnosis_message_id"`
	DiagnosisPayloadSHA256 string           `json:"diagnosis_payload_sha256"`
}

// WO-62/WO-72: a lost-response retry must reconcile to already-finalized
// local truth for the exact finalized diagnosis/receipt pair before Hivebus
// creates a second Workledger side effect or failed pending lane.
func existingPromotedRetry(
	snapshot store.ThreadSnapshot,
	diagnosisEnvelope model.Envelope,
	draft work.Draft,
) (WorkOrderRef, bool) {
	hasDiagnosis := false
	var canonical WorkOrderRef
	for _, envelope := range snapshot.Envelopes {
		switch envelope.Type {
		case model.MessageTypeDiagnosisPropose:
			if promotedDiagnosisMatchesRetry(envelope, diagnosisEnvelope) {
				hasDiagnosis = true
			}
		case model.MessageTypeWorkOrderCreate:
			if ref, ok := promotedWorkOrderMatchesRetry(envelope, diagnosisEnvelope, draft); ok {
				canonical = ref
			}
		}
	}
	if !hasDiagnosis || canonical.ID <= 0 {
		return existingLegacyPromotedRetry(snapshot, diagnosisEnvelope, draft)
	}

	return canonical, true
}

// WO-74: pre-WO-72 receipts did not carry diagnosis binding fields, so they
// may satisfy a duplicate retry only when the whole finalized thread is
// unambiguous. Multi-diagnosis or multi-receipt legacy threads fail closed.
func existingLegacyPromotedRetry(
	snapshot store.ThreadSnapshot,
	diagnosisEnvelope model.Envelope,
	draft work.Draft,
) (WorkOrderRef, bool) {
	validDiagnosisCount := 0
	hasExactDiagnosis := false
	receiptCount := 0
	var canonical WorkOrderRef

	for _, envelope := range snapshot.Envelopes {
		switch envelope.Type {
		case model.MessageTypeDiagnosisPropose:
			if !legacyPromotedDiagnosisCandidate(envelope) {
				continue
			}
			validDiagnosisCount++
			if promotedDiagnosisMatchesRetry(envelope, diagnosisEnvelope) {
				hasExactDiagnosis = true
			}
		case model.MessageTypeWorkOrderCreate:
			receipt, relevant, valid := promotedWorkOrderCandidate(envelope, draft)
			if !relevant {
				continue
			}
			receiptCount++
			if !valid {
				continue
			}
			if receipt.DiagnosisMessageID == "" &&
				receipt.DiagnosisPayloadSHA256 == "" &&
				promotedRetryReceiptMatchesDraft(receipt, draft) {
				canonical = WorkOrderRef{
					Project: strings.TrimSpace(receipt.WorkledgerProject),
					ID:      receipt.WorkOrderID,
					Title:   receipt.WorkOrderTitle,
				}
			}
		}
	}

	if validDiagnosisCount != 1 ||
		!hasExactDiagnosis ||
		receiptCount != 1 ||
		canonical.ID <= 0 {
		return WorkOrderRef{}, false
	}

	return canonical, true
}

// WO-74: legacy fallback counts only semantically valid promotion-passed
// diagnoses, then requires exactly one candidate before trusting the retry.
func legacyPromotedDiagnosisCandidate(envelope model.Envelope) bool {
	if envelope.Type != model.MessageTypeDiagnosisPropose ||
		!envelope.Trace.Verified ||
		envelope.Trace.PromotionStatus != model.PromotionStatusPassed {
		return false
	}

	var diagnosis model.Diagnosis
	if err := json.Unmarshal(envelope.Payload, &diagnosis); err != nil {
		return false
	}
	if err := diagnosis.Validate(); err != nil {
		return false
	}

	return diagnosis.Verified && len(diagnosis.MissingInfo) == 0
}

func promotedDiagnosisMatchesRetry(envelope model.Envelope, expected model.Envelope) bool {
	return envelope.ThreadID == expected.ThreadID &&
		envelope.MessageID == expected.MessageID &&
		envelope.Type == model.MessageTypeDiagnosisPropose &&
		envelope.Trace.Verified &&
		envelope.Trace.PromotionStatus == model.PromotionStatusPassed &&
		string(envelope.Payload) == string(expected.Payload)
}

func promotedWorkOrderMatchesRetry(
	envelope model.Envelope,
	diagnosisEnvelope model.Envelope,
	draft work.Draft,
) (WorkOrderRef, bool) {
	if envelope.Type != model.MessageTypeWorkOrderCreate ||
		!envelope.Trace.Verified ||
		envelope.Trace.PromotionStatus != model.PromotionStatusPassed {
		return WorkOrderRef{}, false
	}

	var receipt promotedRetryReceipt
	if err := json.Unmarshal(envelope.Payload, &receipt); err != nil {
		return WorkOrderRef{}, false
	}
	if !promotedRetryReceiptMatchesDraft(receipt, draft) {
		return WorkOrderRef{}, false
	}
	if receipt.DiagnosisMessageID != diagnosisEnvelope.MessageID ||
		receipt.DiagnosisPayloadSHA256 != promotionPayloadSHA256(diagnosisEnvelope.Payload) {
		return WorkOrderRef{}, false
	}

	return WorkOrderRef{
		Project: strings.TrimSpace(receipt.WorkledgerProject),
		ID:      receipt.WorkOrderID,
		Title:   receipt.WorkOrderTitle,
	}, true
}

// WO-74/WO-77: count every promotion-passed Workledger receipt-shaped fact for
// the thread/project. Malformed or mismatched receipts make the legacy lane
// ambiguous instead of disappearing from fallback accounting.
func promotedWorkOrderCandidate(
	envelope model.Envelope,
	draft work.Draft,
) (promotedRetryReceipt, bool, bool) {
	if envelope.Type != model.MessageTypeWorkOrderCreate ||
		!envelope.Trace.Verified ||
		envelope.Trace.PromotionStatus != model.PromotionStatusPassed {
		return promotedRetryReceipt{}, false, false
	}
	if envelope.ThreadID != draft.SourceThreadID {
		return promotedRetryReceipt{}, true, false
	}

	var receipt promotedRetryReceipt
	if err := json.Unmarshal(envelope.Payload, &receipt); err != nil {
		return promotedRetryReceipt{}, true, false
	}
	if strings.TrimSpace(receipt.TrackingSystem) != "workledger" {
		return receipt, true, false
	}
	if strings.TrimSpace(receipt.WorkledgerProject) != strings.TrimSpace(draft.WorkledgerProject) {
		return receipt, true, false
	}
	if receipt.SourceThreadID != draft.SourceThreadID {
		return receipt, true, false
	}
	if receipt.WorkOrderID <= 0 || strings.TrimSpace(receipt.WorkOrderTitle) == "" {
		return receipt, true, false
	}

	return receipt, true, true
}

func promotedRetryReceiptMatchesDraft(receipt promotedRetryReceipt, draft work.Draft) bool {
	if strings.TrimSpace(receipt.TrackingSystem) != "workledger" {
		return false
	}
	if strings.TrimSpace(receipt.WorkledgerProject) != strings.TrimSpace(draft.WorkledgerProject) {
		return false
	}
	if receipt.SourceThreadID != draft.SourceThreadID {
		return false
	}
	if receipt.WorkOrderID <= 0 || strings.TrimSpace(receipt.WorkOrderTitle) != strings.TrimSpace(draft.Title) {
		return false
	}
	return receipt.Confidence == draft.Confidence &&
		slices.Equal(receipt.EvidenceIDs, draft.EvidenceIDs) &&
		slices.Equal(receipt.OptionalSyncTargets, draft.OptionalSyncTargets)
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

	digest := promotionDiagnosisDigest(threadID, request)
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
			CorrelationID:   threadID,
			SpanID:          "promote.diagnosis",
			Model:           "hivebus",
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed, // WO-54: recovery trusts only promotion-passed diagnosis envelopes
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
	diagnosisPayload, err := json.Marshal(request.Diagnosis)
	if err != nil {
		return model.Envelope{}, fmt.Errorf("marshal diagnosis binding payload: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"tracking_system":          draft.TrackingSystem,
		"workledger_project":       draft.WorkledgerProject,
		"work_order_id":            canonical.ID,
		"work_order_title":         canonical.Title,
		"source_thread_id":         draft.SourceThreadID,
		"optional_sync_targets":    request.OptionalSyncTargets,
		"confidence":               draft.Confidence,
		"evidence_ids":             draft.EvidenceIDs,
		"diagnosis_message_id":     promotionDiagnosisMessageID(threadID, request),
		"diagnosis_payload_sha256": promotionPayloadSHA256(diagnosisPayload),
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
			CorrelationID:   threadID,
			SpanID:          "promote.work_order",
			Model:           "hivebus",
			Verified:        true,
			PromotionStatus: model.PromotionStatusPassed, // WO-54: work-order receipts only enter verified recovery state after promotion passes
		},
		Security: model.Security{
			Scheme:    "ed25519",
			Nonce:     "nonce_work_order_" + digest,
			Signature: "sig_work_order_" + digest,
			Signed:    true,
		},
	}, nil
}

func buildPendingPromotionRecord(
	envelope model.Envelope,
	at time.Time,
) store.PromotionPendingRecord {
	pendingEnvelope := envelope
	pendingEnvelope.Trace.PromotionStatus = model.PromotionStatusPending

	return store.PromotionPendingRecord{
		PendingMessageID: "pending_" + envelope.MessageID,
		Envelope:         pendingEnvelope,
		Status:           model.PromotionStatusPending,
		UpdatedAt:        at,
	}
}

func recordFailedPendingPromotion(
	ctx context.Context,
	st *store.Store,
	record store.PromotionPendingRecord,
	failure error,
	at time.Time,
) error {
	record.Envelope.Trace.PromotionStatus = model.PromotionStatusFailed
	record.Status = model.PromotionStatusFailed
	record.FailureReason = failure.Error()
	record.UpdatedAt = at

	err := st.RecordPromotionPending(ctx, record)
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

func promotionDiagnosisDigest(threadID string, request promoteThreadRequest) string {
	return stableDigest(threadID, request.WorkledgerProject, request.Diagnosis.Problem)
}

func promotionDiagnosisMessageID(threadID string, request promoteThreadRequest) string {
	return "msg_diagnosis_" + promotionDiagnosisDigest(threadID, request)
}

func promotionPayloadSHA256(payload []byte) string {
	hash := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(hash[:])
}
