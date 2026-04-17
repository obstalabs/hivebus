package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

var (
	ErrClarificationPending = errors.New("clarification is already pending")
	ErrClarificationStale   = errors.New("clarification is no longer pending")
	ErrClarificationExpired = errors.New("clarification deadline has expired")
)

func (s *Store) AppendClarificationRequest(
	ctx context.Context,
	leaseID string,
	workerID string,
	request model.Envelope,
	now time.Time,
) error {
	if strings.TrimSpace(leaseID) == "" {
		return errors.New("lease_id is required")
	}
	if strings.TrimSpace(workerID) == "" {
		return errors.New("worker_id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin clarification request transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := expireStaleLeasesTx(ctx, tx, now); err != nil {
		return err
	}

	lease, err := loadLeaseTx(ctx, tx, leaseID)
	if err != nil {
		return err
	}
	if lease.WorkerID != workerID {
		return ErrLeaseNotOwned
	}
	switch lease.Status {
	case LeaseStatusExpired:
		return ErrLeaseExpired
	case LeaseStatusCompleted:
		return ErrLeaseFinalized
	case LeaseStatusActive:
	default:
		return fmt.Errorf("unsupported lease status %q", lease.Status)
	}

	taskRequest, err := loadTaskEnvelopeTx(ctx, tx, lease.TaskMessageID)
	if err != nil {
		return err
	}
	if err := request.ValidateClarificationRequest(taskRequest); err != nil {
		return err
	}
	if request.From != workerID {
		return errors.New("clarification.request from must match worker_id")
	}

	snapshot, err := loadThreadSnapshotTx(ctx, tx, lease.ThreadID)
	if err != nil {
		return err
	}
	state, err := snapshot.Thread.PendingClarification(snapshot.Envelopes, now)
	if err != nil {
		return err
	}
	if state != nil {
		return ErrClarificationPending
	}

	if err := insertEnvelopeEventTx(ctx, tx, request); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit clarification request: %w", err)
	}

	return nil
}

func (s *Store) AppendClarificationResponse(
	ctx context.Context,
	threadID string,
	response model.Envelope,
	now time.Time,
) error {
	if strings.TrimSpace(threadID) == "" {
		return errors.New("thread_id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin clarification response transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	snapshot, err := loadThreadSnapshotTx(ctx, tx, threadID)
	if err != nil {
		return err
	}
	state, err := snapshot.Thread.PendingClarification(snapshot.Envelopes, now)
	if err != nil {
		return err
	}
	if state == nil {
		return ErrClarificationStale
	}
	if err := response.ValidateClarificationResponse(state.TaskRequest, state.Request); err != nil {
		return err
	}
	if state.Expired {
		return ErrClarificationExpired
	}
	if strings.TrimSpace(response.ThreadID) != threadID {
		return errors.New("clarification.response thread_id must match request path")
	}

	if err := insertEnvelopeEventTx(ctx, tx, response); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit clarification response: %w", err)
	}

	return nil
}

func loadThreadSnapshotTx(ctx context.Context, tx *sql.Tx, threadID string) (ThreadSnapshot, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT event_kind, payload_json
		FROM thread_events
		WHERE thread_id = ?
		ORDER BY sequence ASC
	`, threadID)
	if err != nil {
		return ThreadSnapshot{}, fmt.Errorf("query thread events: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return loadThreadSnapshotRows(rows, threadID)
}
