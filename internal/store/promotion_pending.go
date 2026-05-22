package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

const (
	eventKindPromotionPending         = "promotion.pending"
	eventKindPromotionPendingResolved = "promotion.pending.resolved"
)

// PromotionPendingRecord keeps failed or in-flight promotion attempts in a
// non-authoritative lane so recovery cannot treat them as verified state.
type PromotionPendingRecord struct {
	PendingMessageID string                `json:"pending_message_id"`       // WO-54: separate pending namespace for promotion attempts
	Envelope         model.Envelope        `json:"envelope"`                 // WO-54: candidate promotion envelope kept out of the verified lane
	Status           model.PromotionStatus `json:"status"`                   // WO-54: pending or failed state for recovery-safe diagnostics
	FailureReason    string                `json:"failure_reason,omitempty"` // WO-54: operator-visible reason the promotion did not complete
	UpdatedAt        time.Time             `json:"updated_at"`               // WO-54: latest status transition for this pending record
}

func (r PromotionPendingRecord) Validate() error {
	switch {
	case strings.TrimSpace(r.PendingMessageID) == "":
		return errors.New("pending_message_id is required")
	case r.UpdatedAt.IsZero():
		return errors.New("updated_at is required")
	case r.Status != model.PromotionStatusPending && r.Status != model.PromotionStatusFailed:
		return fmt.Errorf("unsupported pending promotion status %q", r.Status)
	}
	if err := r.Envelope.Validate(); err != nil {
		return err
	}
	if r.Envelope.Trace.PromotionStatus != r.Status {
		return errors.New("pending promotion envelope trace.promotion_status must match record status")
	}

	return nil
}

type promotionPendingResolution struct {
	PendingMessageID string    `json:"pending_message_id"` // WO-54: resolves the pending lane after verified envelopes are written
	UpdatedAt        time.Time `json:"updated_at"`         // WO-54: resolution timestamp for replay
}

func (r promotionPendingResolution) Validate() error {
	switch {
	case strings.TrimSpace(r.PendingMessageID) == "":
		return errors.New("pending_message_id is required")
	case r.UpdatedAt.IsZero():
		return errors.New("updated_at is required")
	}

	return nil
}

func (s *Store) RecordPromotionPending(ctx context.Context, record PromotionPendingRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if _, err := s.LoadThread(ctx, record.Envelope.ThreadID); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pending promotion transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := insertPromotionPendingEventTx(ctx, tx, record.Envelope.ThreadID, record); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pending promotion transaction: %w", err)
	}

	return nil
}

func (s *Store) FinalizePromotion(
	ctx context.Context,
	pendingMessageID string,
	at time.Time,
	envelopes ...model.Envelope,
) error {
	if strings.TrimSpace(pendingMessageID) == "" {
		return errors.New("pending_message_id is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	threadID, err := validateFinalizePromotionEnvelopes(envelopes)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin promotion finalization transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	snapshot, err := loadThreadSnapshotTx(ctx, tx, threadID)
	if err != nil {
		return err
	}

	foundPending := false
	for _, record := range snapshot.PendingPromotions {
		if record.PendingMessageID == pendingMessageID {
			foundPending = true
			break
		}
	}
	if !foundPending {
		return fmt.Errorf("pending promotion %q not found", pendingMessageID)
	}

	for _, envelope := range envelopes {
		if err := insertEnvelopeEventTx(ctx, tx, envelope); err != nil {
			return err
		}
	}

	if err := insertPromotionPendingResolutionEventTx(ctx, tx, threadID, promotionPendingResolution{
		PendingMessageID: pendingMessageID,
		UpdatedAt:        at,
	}); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit promotion finalization: %w", err)
	}

	return nil
}

// WO-65: pending promotion only resolves once the complete authoritative pair
// is present, so recovery never inherits a half-promoted thread.
func validateFinalizePromotionEnvelopes(envelopes []model.Envelope) (string, error) {
	if len(envelopes) != 2 {
		return "", errors.New("verified promotion finalization requires exactly one diagnosis.proposed envelope and one work_order.create envelope")
	}

	threadID := strings.TrimSpace(envelopes[0].ThreadID)
	if threadID == "" {
		return "", errors.New("verified promotion envelope thread_id is required")
	}

	hasDiagnosis := false
	hasWorkOrder := false
	for _, envelope := range envelopes {
		if envelope.ThreadID != threadID {
			return "", errors.New("verified promotion envelopes must share a thread_id")
		}
		if !envelope.Trace.Verified {
			return "", errors.New("verified promotion envelopes must carry trace.verified=true")
		}
		if envelope.Trace.PromotionStatus != model.PromotionStatusPassed {
			return "", errors.New("verified promotion envelopes must carry trace.promotion_status=passed")
		}

		switch envelope.Type {
		case model.MessageTypeDiagnosisPropose:
			if hasDiagnosis {
				return "", errors.New("verified promotion finalization accepts only one diagnosis.proposed envelope")
			}
			if err := validateFinalizedDiagnosisPayload(envelope); err != nil {
				return "", err
			}
			hasDiagnosis = true
		case model.MessageTypeWorkOrderCreate:
			if hasWorkOrder {
				return "", errors.New("verified promotion finalization accepts only one work_order.create envelope")
			}
			if err := validateFinalizedWorkOrderPayload(threadID, envelope); err != nil {
				return "", err
			}
			hasWorkOrder = true
		default:
			return "", errors.New("verified promotion finalization only accepts diagnosis.proposed and work_order.create envelopes")
		}
	}
	if !hasDiagnosis || !hasWorkOrder {
		return "", errors.New("verified promotion finalization requires both diagnosis.proposed and work_order.create envelopes")
	}

	return threadID, nil
}

// WO-65: trace-valid diagnosis envelopes must also carry recovery-safe payload
// truth before they are promoted out of the pending lane.
func validateFinalizedDiagnosisPayload(envelope model.Envelope) error {
	var diagnosis model.Diagnosis
	if err := json.Unmarshal(envelope.Payload, &diagnosis); err != nil {
		return fmt.Errorf("invalid verified promotion diagnosis payload: %w", err)
	}
	if err := diagnosis.Validate(); err != nil {
		return fmt.Errorf("invalid verified promotion diagnosis: %w", err)
	}
	if !diagnosis.Verified {
		return errors.New("verified promotion diagnosis payload must carry verified=true")
	}
	if len(diagnosis.MissingInfo) > 0 {
		return errors.New("verified promotion diagnosis payload must not contain missing_info")
	}

	return nil
}

// WO-65: the work-order receipt must point back at this exact thread before
// recovery can treat it as the authoritative promotion receipt.
func validateFinalizedWorkOrderPayload(threadID string, envelope model.Envelope) error {
	var payload promotedWorkOrderPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return fmt.Errorf("invalid verified promotion work_order.create payload: %w", err)
	}
	if strings.TrimSpace(payload.TrackingSystem) == "" {
		return errors.New("verified promotion work_order.create tracking_system is required")
	}
	if strings.TrimSpace(payload.SourceThreadID) == "" {
		return errors.New("verified promotion work_order.create source_thread_id is required")
	}
	if payload.SourceThreadID != threadID {
		return errors.New("verified promotion work_order.create source_thread_id does not match thread")
	}

	return nil
}

func insertPromotionPendingEventTx(
	ctx context.Context,
	tx *sql.Tx,
	threadID string,
	record PromotionPendingRecord,
) error {
	if err := record.Validate(); err != nil {
		return err
	}

	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal pending promotion: %w", err)
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO thread_events (thread_id, event_kind, event_at, payload_json)
		 VALUES (?, ?, ?, ?)`,
		threadID,
		eventKindPromotionPending,
		formatTime(record.UpdatedAt),
		payload,
	)
	if err != nil {
		return fmt.Errorf("insert pending promotion event: %w", err)
	}

	return nil
}

func insertPromotionPendingResolutionEventTx(
	ctx context.Context,
	tx *sql.Tx,
	threadID string,
	resolution promotionPendingResolution,
) error {
	if err := resolution.Validate(); err != nil {
		return err
	}

	payload, err := json.Marshal(resolution)
	if err != nil {
		return fmt.Errorf("marshal pending promotion resolution: %w", err)
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO thread_events (thread_id, event_kind, event_at, payload_json)
		 VALUES (?, ?, ?, ?)`,
		threadID,
		eventKindPromotionPendingResolved,
		formatTime(resolution.UpdatedAt),
		payload,
	)
	if err != nil {
		return fmt.Errorf("insert pending promotion resolution event: %w", err)
	}

	return nil
}
