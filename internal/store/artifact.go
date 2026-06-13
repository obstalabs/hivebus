package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/obstalabs/hivebus/internal/model"
)

const eventKindArtifactRecorded = "artifact.recorded"

var ErrDuplicateArtifactID = errors.New("artifact already exists in thread")

func (s *Store) AppendArtifact(ctx context.Context, threadID string, artifact model.Artifact, at string) error {
	if strings.TrimSpace(threadID) == "" {
		return errors.New("thread_id is required")
	}
	if err := artifact.Validate(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	snapshot, err := s.LoadThread(ctx, threadID)
	if err != nil {
		return err
	}
	for _, existing := range snapshot.Thread.Evidence {
		if existing.ArtifactID == artifact.ArtifactID {
			return ErrDuplicateArtifactID
		}
	}

	payload, err := json.Marshal(artifact)
	if err != nil {
		return fmt.Errorf("marshal artifact: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO thread_events (thread_id, event_kind, event_at, payload_json)
		VALUES (?, ?, ?, ?)
	`,
		threadID,
		eventKindArtifactRecorded,
		at,
		payload,
	)
	if err != nil {
		return fmt.Errorf("insert artifact event: %w", err)
	}

	return nil
}
