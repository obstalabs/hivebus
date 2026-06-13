package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
)

func (s *Store) CreateIntake(
	ctx context.Context,
	thread model.Thread,
	artifacts []model.Artifact,
	initialTask model.Envelope,
) (ThreadSnapshot, error) {
	if err := thread.Validate(); err != nil {
		return ThreadSnapshot{}, err
	}
	if err := initialTask.ValidateTaskRequest(); err != nil {
		return ThreadSnapshot{}, err
	}
	if initialTask.ThreadID != thread.ThreadID {
		return ThreadSnapshot{}, errors.New("initial task thread_id must match thread thread_id")
	}
	if initialTask.From != "" && !participantExists(thread.Participants, initialTask.From) {
		return ThreadSnapshot{}, errors.New("initial task from must match a thread participant")
	}
	if err := validateReferencedArtifacts(artifacts, initialTask.ArtifactIDs); err != nil {
		return ThreadSnapshot{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ThreadSnapshot{}, fmt.Errorf("begin intake transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := insertThreadCreatedEventTx(ctx, tx, thread); err != nil {
		return ThreadSnapshot{}, err
	}
	for _, artifact := range artifacts {
		if err := insertArtifactEventTx(ctx, tx, thread.ThreadID, artifact, thread.CreatedAt); err != nil {
			return ThreadSnapshot{}, err
		}
	}
	if err := insertEnvelopeEventTx(ctx, tx, initialTask); err != nil {
		return ThreadSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return ThreadSnapshot{}, fmt.Errorf("commit intake: %w", err)
	}

	return s.LoadThread(ctx, thread.ThreadID)
}

func insertThreadCreatedEventTx(ctx context.Context, tx *sql.Tx, thread model.Thread) error {
	payload, err := json.Marshal(thread)
	if err != nil {
		return fmt.Errorf("marshal thread: %w", err)
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO thread_events (thread_id, event_kind, event_at, payload_json)
		 VALUES (?, ?, ?, ?)`,
		thread.ThreadID,
		eventKindThreadCreated,
		formatTime(thread.CreatedAt),
		payload,
	)
	if err != nil {
		return mapInsertError(err)
	}

	return nil
}

func insertArtifactEventTx(
	ctx context.Context,
	tx *sql.Tx,
	threadID string,
	artifact model.Artifact,
	at time.Time,
) error {
	if err := artifact.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(artifact)
	if err != nil {
		return fmt.Errorf("marshal artifact: %w", err)
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO thread_events (thread_id, event_kind, event_at, payload_json)
		 VALUES (?, ?, ?, ?)`,
		threadID,
		eventKindArtifactRecorded,
		formatTime(at),
		payload,
	)
	if err != nil {
		return fmt.Errorf("insert artifact event: %w", err)
	}

	return nil
}

func validateReferencedArtifacts(artifacts []model.Artifact, referenced []string) error {
	if len(referenced) == 0 {
		referenced = nil
	}

	available := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		if _, exists := available[artifact.ArtifactID]; exists {
			return fmt.Errorf("duplicate artifact %q", artifact.ArtifactID)
		}
		available[artifact.ArtifactID] = struct{}{}
	}

	for _, artifactID := range referenced {
		if _, ok := available[strings.TrimSpace(artifactID)]; !ok {
			return fmt.Errorf("initial task references unknown artifact %q", artifactID)
		}
	}

	return nil
}

func participantExists(participants []model.Participant, participantID string) bool {
	for _, participant := range participants {
		if participant.ID == participantID {
			return true
		}
	}

	return false
}
