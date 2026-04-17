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

type ThreadTransitionEvent struct {
	Status    model.ThreadStatus `json:"status"`
	UpdatedAt time.Time          `json:"updated_at"`
}

func (e ThreadTransitionEvent) Validate() error {
	if e.UpdatedAt.IsZero() {
		return errors.New("updated_at is required")
	}
	switch e.Status {
	case model.ThreadStatusReported,
		model.ThreadStatusCollecting,
		model.ThreadStatusInvestigating,
		model.ThreadStatusWaiting,
		model.ThreadStatusReadyForWork,
		model.ThreadStatusDone,
		model.ThreadStatusFailed,
		model.ThreadStatusCancelled:
	default:
		return fmt.Errorf("unsupported status %q", e.Status)
	}
	return nil
}

func (s *Store) TransitionThread(
	ctx context.Context,
	threadID string,
	next model.ThreadStatus,
	at time.Time,
) (ThreadSnapshot, error) {
	if strings.TrimSpace(threadID) == "" {
		return ThreadSnapshot{}, errors.New("thread_id is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ThreadSnapshot{}, fmt.Errorf("begin thread transition transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	snapshot, err := loadThreadSnapshotTx(ctx, tx, threadID)
	if err != nil {
		return ThreadSnapshot{}, err
	}
	if err := snapshot.Thread.Transition(next, at); err != nil {
		return ThreadSnapshot{}, err
	}

	event := ThreadTransitionEvent{
		Status:    next,
		UpdatedAt: at,
	}
	if err := insertThreadTransitionEventTx(ctx, tx, threadID, event); err != nil {
		return ThreadSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return ThreadSnapshot{}, fmt.Errorf("commit thread transition: %w", err)
	}

	return s.LoadThread(ctx, threadID)
}

func insertThreadTransitionEventTx(
	ctx context.Context,
	tx *sql.Tx,
	threadID string,
	event ThreadTransitionEvent,
) error {
	if err := event.Validate(); err != nil {
		return err
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal thread transition: %w", err)
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO thread_events (thread_id, event_kind, event_at, payload_json)
		 VALUES (?, ?, ?, ?)`,
		threadID,
		eventKindThreadTransition,
		formatTime(event.UpdatedAt),
		payload,
	)
	if err != nil {
		return fmt.Errorf("insert thread transition event: %w", err)
	}

	return nil
}
