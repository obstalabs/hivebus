package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
	_ "modernc.org/sqlite"
)

const (
	sqliteDriver              = "sqlite"
	eventKindThreadCreated    = "thread.created"
	eventKindEnvelopeAppended = "envelope.appended"
)

var (
	ErrThreadNotFound          = errors.New("thread not found")
	ErrDuplicateThread         = errors.New("thread already exists")
	ErrDuplicateMessage        = errors.New("message already exists")
	ErrDuplicateIdempotencyKey = errors.New("idempotency key already exists")
)

type Store struct {
	db *sql.DB
}

type ThreadSnapshot struct {
	Thread        model.Thread     `json:"thread"`
	Envelopes     []model.Envelope `json:"envelopes"`
	LeaseReceipts []LeaseReceipt   `json:"lease_receipts,omitempty"`
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("path is required")
	}

	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	db, err := sql.Open(
		sqliteDriver,
		path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)",
	)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := migrate(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("store is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	return s.db.PingContext(ctx)
}

func (s *Store) AppendThread(ctx context.Context, thread model.Thread) (ThreadSnapshot, error) {
	if err := thread.Validate(); err != nil {
		return ThreadSnapshot{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	payload, err := json.Marshal(thread)
	if err != nil {
		return ThreadSnapshot{}, fmt.Errorf("marshal thread: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO thread_events (thread_id, event_kind, event_at, payload_json)
		VALUES (?, ?, ?, ?)
	`,
		thread.ThreadID,
		eventKindThreadCreated,
		formatTime(thread.CreatedAt),
		payload,
	)
	if err != nil {
		return ThreadSnapshot{}, mapInsertError(err)
	}

	return ThreadSnapshot{Thread: thread}, nil
}

func (s *Store) AppendEnvelope(ctx context.Context, envelope model.Envelope) error {
	if err := envelope.Validate(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if _, err := s.LoadThread(ctx, envelope.ThreadID); err != nil {
		return err
	}
	if exists, err := s.eventExists(ctx, "message_id = ?", envelope.MessageID); err != nil {
		return err
	} else if exists {
		return ErrDuplicateMessage
	}
	if exists, err := s.eventExists(
		ctx,
		"thread_id = ? AND idempotency_key = ?",
		envelope.ThreadID,
		envelope.IdempotencyKey,
	); err != nil {
		return err
	} else if exists {
		return ErrDuplicateIdempotencyKey
	}

	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO thread_events (thread_id, event_kind, message_id, idempotency_key, event_at, payload_json)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		envelope.ThreadID,
		eventKindEnvelopeAppended,
		envelope.MessageID,
		envelope.IdempotencyKey,
		formatTime(envelope.SentAt),
		payload,
	)
	if err != nil {
		return mapInsertError(err)
	}

	return nil
}

func (s *Store) LoadThread(ctx context.Context, threadID string) (ThreadSnapshot, error) {
	if s == nil || s.db == nil {
		return ThreadSnapshot{}, errors.New("store is not initialized")
	}
	if strings.TrimSpace(threadID) == "" {
		return ThreadSnapshot{}, errors.New("thread_id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	rows, err := s.db.QueryContext(ctx, `
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

func loadThreadSnapshotRows(
	rows *sql.Rows,
	threadID string,
) (ThreadSnapshot, error) {
	var snapshot ThreadSnapshot
	foundThread := false
	for rows.Next() {
		var eventKind string
		var payload []byte
		if err := rows.Scan(&eventKind, &payload); err != nil {
			return ThreadSnapshot{}, fmt.Errorf("scan thread event: %w", err)
		}

		switch eventKind {
		case eventKindThreadCreated:
			if foundThread {
				return ThreadSnapshot{}, errors.New("thread replay contains duplicate thread.created event")
			}

			if err := json.Unmarshal(payload, &snapshot.Thread); err != nil {
				return ThreadSnapshot{}, fmt.Errorf("unmarshal thread event: %w", err)
			}
			if err := snapshot.Thread.Validate(); err != nil {
				return ThreadSnapshot{}, fmt.Errorf("invalid thread event: %w", err)
			}
			foundThread = true
		case eventKindEnvelopeAppended:
			var envelope model.Envelope
			if err := json.Unmarshal(payload, &envelope); err != nil {
				return ThreadSnapshot{}, fmt.Errorf("unmarshal envelope event: %w", err)
			}
			if err := envelope.Validate(); err != nil {
				return ThreadSnapshot{}, fmt.Errorf("invalid envelope event: %w", err)
			}
			if envelope.ThreadID != threadID {
				return ThreadSnapshot{}, errors.New("envelope thread_id does not match replay target")
			}
			snapshot.Envelopes = append(snapshot.Envelopes, envelope)
		case eventKindWorkerLease:
			var receipt LeaseReceipt
			if err := json.Unmarshal(payload, &receipt); err != nil {
				return ThreadSnapshot{}, fmt.Errorf("unmarshal lease receipt event: %w", err)
			}
			if receipt.ThreadID != threadID {
				return ThreadSnapshot{}, errors.New("lease receipt thread_id does not match replay target")
			}
			snapshot.LeaseReceipts = append(snapshot.LeaseReceipts, receipt)
		case eventKindArtifactRecorded:
			var artifact model.Artifact
			if err := json.Unmarshal(payload, &artifact); err != nil {
				return ThreadSnapshot{}, fmt.Errorf("unmarshal artifact event: %w", err)
			}
			if err := artifact.Validate(); err != nil {
				return ThreadSnapshot{}, fmt.Errorf("invalid artifact event: %w", err)
			}
			for _, existing := range snapshot.Thread.Evidence {
				if existing.ArtifactID == artifact.ArtifactID {
					return ThreadSnapshot{}, fmt.Errorf("duplicate artifact event %q", artifact.ArtifactID)
				}
			}
			snapshot.Thread.Evidence = append(snapshot.Thread.Evidence, artifact)
		default:
			return ThreadSnapshot{}, fmt.Errorf("unsupported event kind %q", eventKind)
		}
	}

	if err := rows.Err(); err != nil {
		return ThreadSnapshot{}, fmt.Errorf("iterate thread events: %w", err)
	}

	if !foundThread {
		return ThreadSnapshot{}, ErrThreadNotFound
	}

	return snapshot, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS thread_events (
			sequence INTEGER PRIMARY KEY AUTOINCREMENT,
			thread_id TEXT NOT NULL,
			event_kind TEXT NOT NULL,
			message_id TEXT,
			idempotency_key TEXT,
			event_at TEXT NOT NULL,
			payload_json BLOB NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS thread_events_thread_created_unique
			ON thread_events(thread_id, event_kind)
			WHERE event_kind = 'thread.created'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS thread_events_message_id_unique
			ON thread_events(message_id)
			WHERE message_id IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS thread_events_idempotency_unique
			ON thread_events(thread_id, idempotency_key)
			WHERE idempotency_key IS NOT NULL AND idempotency_key <> ''`,
		`CREATE INDEX IF NOT EXISTS thread_events_thread_sequence_idx
			ON thread_events(thread_id, sequence)`,
		`CREATE TABLE IF NOT EXISTS worker_leases (
			lease_id TEXT PRIMARY KEY,
			thread_id TEXT NOT NULL,
			task_message_id TEXT NOT NULL,
			accepted_message_id TEXT NOT NULL,
			worker_id TEXT NOT NULL,
			claimed_at TEXT NOT NULL,
			leased_until TEXT NOT NULL,
			status TEXT NOT NULL,
			result_message_id TEXT,
			completed_at TEXT
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS worker_leases_active_task_unique
			ON worker_leases(task_message_id)
			WHERE status = 'active'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS worker_leases_active_worker_unique
			ON worker_leases(worker_id)
			WHERE status = 'active'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS worker_leases_completed_task_unique
			ON worker_leases(task_message_id)
			WHERE status = 'completed'`,
	}

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate sqlite store: %w", err)
		}
	}

	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func mapInsertError(err error) error {
	if err == nil {
		return nil
	}

	message := err.Error()
	switch {
	case strings.Contains(message, "thread_events.thread_id, thread_events.event_kind"):
		return ErrDuplicateThread
	case strings.Contains(message, "thread_events.message_id"):
		return ErrDuplicateMessage
	case strings.Contains(message, "thread_events.thread_id, thread_events.idempotency_key"):
		return ErrDuplicateIdempotencyKey
	default:
		return err
	}
}

func (s *Store) eventExists(ctx context.Context, predicate string, args ...any) (bool, error) {
	query := "SELECT 1 FROM thread_events WHERE " + predicate + " LIMIT 1"

	row := s.db.QueryRowContext(ctx, query, args...)
	var marker int
	if err := row.Scan(&marker); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("query event existence: %w", err)
	}

	return true, nil
}
