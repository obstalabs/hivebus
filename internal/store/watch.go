package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

type ThreadEvent struct {
	Sequence     int64           `json:"sequence"`
	ThreadID     string          `json:"thread_id"`
	EventKind    string          `json:"event_kind"`
	EventAt      time.Time       `json:"event_at"`
	Envelope     *model.Envelope `json:"envelope,omitempty"`
	Thread       *model.Thread   `json:"thread,omitempty"`
	Artifact     *model.Artifact `json:"artifact,omitempty"`
	LeaseReceipt *LeaseReceipt   `json:"lease_receipt,omitempty"`
}

func (s *Store) ListThreadEvents(
	ctx context.Context,
	threadID string,
	afterSequence int64,
	limit int,
) ([]ThreadEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store is not initialized")
	}
	if strings.TrimSpace(threadID) == "" {
		return nil, errors.New("thread_id is required")
	}
	if afterSequence < 0 {
		return nil, errors.New("after_sequence must be zero or positive")
	}
	if limit <= 0 {
		limit = 128
	}
	if ctx == nil {
		ctx = context.Background()
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence, event_kind, event_at, payload_json
		FROM thread_events
		WHERE thread_id = ? AND sequence > ?
		ORDER BY sequence ASC
		LIMIT ?
	`, threadID, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("query thread events: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	events := make([]ThreadEvent, 0, limit)
	for rows.Next() {
		var event ThreadEvent
		var eventAt string
		var payload []byte
		if err := rows.Scan(&event.Sequence, &event.EventKind, &eventAt, &payload); err != nil {
			return nil, fmt.Errorf("scan thread event: %w", err)
		}
		event.ThreadID = threadID
		event.EventAt = parseTime(eventAt)

		switch event.EventKind {
		case eventKindThreadCreated:
			var thread model.Thread
			if err := json.Unmarshal(payload, &thread); err != nil {
				return nil, fmt.Errorf("unmarshal thread event: %w", err)
			}
			event.Thread = &thread
		case eventKindEnvelopeAppended:
			var envelope model.Envelope
			if err := json.Unmarshal(payload, &envelope); err != nil {
				return nil, fmt.Errorf("unmarshal envelope event: %w", err)
			}
			event.Envelope = &envelope
		case eventKindArtifactRecorded:
			var artifact model.Artifact
			if err := json.Unmarshal(payload, &artifact); err != nil {
				return nil, fmt.Errorf("unmarshal artifact event: %w", err)
			}
			event.Artifact = &artifact
		case eventKindWorkerLease:
			var receipt LeaseReceipt
			if err := json.Unmarshal(payload, &receipt); err != nil {
				return nil, fmt.Errorf("unmarshal lease receipt event: %w", err)
			}
			event.LeaseReceipt = &receipt
		default:
			return nil, fmt.Errorf("unsupported event kind %q", event.EventKind)
		}

		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate thread events: %w", err)
	}

	if len(events) == 0 {
		if _, err := s.LoadThread(ctx, threadID); err != nil {
			return nil, err
		}
	}

	return events, nil
}
