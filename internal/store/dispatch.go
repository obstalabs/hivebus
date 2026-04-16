package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ppiankov/hivebus/internal/model"
)

func (s *Store) CreateDispatch(
	ctx context.Context,
	thread model.Thread,
	envelope model.Envelope,
) (ThreadSnapshot, error) {
	if err := thread.Validate(); err != nil {
		return ThreadSnapshot{}, err
	}
	if err := envelope.ValidateTaskRequest(); err != nil {
		return ThreadSnapshot{}, err
	}
	if envelope.ThreadID != thread.ThreadID {
		return ThreadSnapshot{}, errors.New("dispatch envelope thread_id must match thread")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ThreadSnapshot{}, fmt.Errorf("begin dispatch transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := insertThreadCreatedTx(ctx, tx, thread); err != nil {
		return ThreadSnapshot{}, err
	}
	if err := insertEnvelopeEventTx(ctx, tx, envelope); err != nil {
		return ThreadSnapshot{}, err
	}

	if err := tx.Commit(); err != nil {
		return ThreadSnapshot{}, fmt.Errorf("commit dispatch: %w", err)
	}

	return ThreadSnapshot{
		Thread:    thread,
		Envelopes: []model.Envelope{envelope},
	}, nil
}

func insertThreadCreatedTx(ctx context.Context, tx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, thread model.Thread) error {
	payload, err := json.Marshal(thread)
	if err != nil {
		return fmt.Errorf("marshal thread: %w", err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO thread_events (thread_id, event_kind, event_at, payload_json)
		 VALUES (?, ?, ?, ?)`,
		thread.ThreadID,
		eventKindThreadCreated,
		formatTime(thread.CreatedAt),
		payload,
	); err != nil {
		return mapInsertError(err)
	}

	return nil
}
