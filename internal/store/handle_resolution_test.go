package store

import (
	"errors"
	"testing"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
)

// handlePayload builds a register payload carrying a stable handle + repository.
func handlePayload(sessionID, participantID, handle, repository string, leaseUntil time.Time) model.AgentSessionPayload {
	payload := sampleAgentSessionPayload(sessionID, participantID, leaseUntil)
	payload.Handle = handle
	payload.Repository = repository
	return payload
}

func registerHandleSession(t *testing.T, st *Store, sessionID, participantID, handle, repository string, leaseUntil, at time.Time) {
	t.Helper()
	if _, err := st.RegisterAgentSession(
		t.Context(),
		handlePayload(sessionID, participantID, handle, repository, leaseUntil),
		model.MessageTypeAgentSessionRegistered,
		at,
	); err != nil {
		t.Fatalf("RegisterAgentSession(%s) error = %v", sessionID, err)
	}
}

// TestResolveTargetHandleRoutesFreshestLiveParticipant is the WO-174 ghost-kill
// anchor: an old session (p1) with an expired lease and a new live session (p2)
// share handle=architect. Resolving the handle MUST pick p2, never the stale p1
// — the exact stale-target ghost the feature kills. Mirrors NR
// TestBroker_SendToHandleRoutesFreshestLiveParticipant.
func TestResolveTargetHandleRoutesFreshestLiveParticipant(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	// p1 registered earlier, lease already expired at resolve time.
	registerHandleSession(t, st, "sess_p1", "participant-p1", "architect", "neurorouter-pro",
		now.Add(-2*time.Minute), now.Add(-5*time.Minute))
	// p2 registered later, live.
	registerHandleSession(t, st, "sess_p2", "participant-p2", "architect", "neurorouter-pro",
		now.Add(30*time.Minute), now.Add(-1*time.Minute))

	resolved, err := st.ResolveTargetHandle(t.Context(), "architect", "neurorouter-pro", now)
	if err != nil {
		t.Fatalf("ResolveTargetHandle() error = %v", err)
	}
	if resolved.ParticipantID != "participant-p2" {
		t.Fatalf("resolved participant = %q, want participant-p2 (freshest live, not stale p1)", resolved.ParticipantID)
	}
	if resolved.SessionID != "sess_p2" {
		t.Fatalf("resolved session = %q, want sess_p2", resolved.SessionID)
	}
	if resolved.StaleMatches != 1 {
		t.Fatalf("stale_matches = %d, want 1 (the expired p1)", resolved.StaleMatches)
	}
}

func TestRegisterAgentSessionHeartbeatPreservesHandleAndRepository(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	registerHandleSession(t, st, "sess_live", "participant-live", "architect", "neurorouter-pro",
		now.Add(30*time.Minute), now)

	heartbeat := sampleAgentSessionPayload("sess_live", "participant-live", now.Add(30*time.Minute))
	session, err := st.RegisterAgentSession(
		t.Context(),
		heartbeat,
		model.MessageTypeAgentSessionHeartbeat,
		now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("RegisterAgentSession(heartbeat omit route key) error = %v", err)
	}
	if session.Handle != "architect" || session.Repository != "neurorouter-pro" {
		t.Fatalf("route key after omitted heartbeat = handle %q repo %q", session.Handle, session.Repository)
	}

	resolved, err := st.ResolveTargetHandle(t.Context(), "architect", "neurorouter-pro", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ResolveTargetHandle(after omitted heartbeat) error = %v", err)
	}
	if resolved.ParticipantID != "participant-live" {
		t.Fatalf("resolved participant = %q, want participant-live", resolved.ParticipantID)
	}

	replacement := sampleAgentSessionPayload("sess_live", "participant-live", now.Add(30*time.Minute))
	replacement.Handle = "reviewer"
	replacement.Repository = "hivebus"
	session, err = st.RegisterAgentSession(
		t.Context(),
		replacement,
		model.MessageTypeAgentSessionHeartbeat,
		now.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatalf("RegisterAgentSession(heartbeat replace route key) error = %v", err)
	}
	if session.Handle != "reviewer" || session.Repository != "hivebus" {
		t.Fatalf("route key after replacement heartbeat = handle %q repo %q", session.Handle, session.Repository)
	}
}

func TestResolveTargetHandleNotFound(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	_, err := st.ResolveTargetHandle(t.Context(), "nobody", "", now)
	if !errors.Is(err, ErrTargetHandleNotFound) {
		t.Fatalf("ResolveTargetHandle() error = %v, want ErrTargetHandleNotFound", err)
	}
}

func TestResolveTargetHandleNotLive(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	// Only a stale session for the handle.
	registerHandleSession(t, st, "sess_stale", "participant-stale", "architect", "",
		now.Add(-1*time.Minute), now.Add(-5*time.Minute))

	resolved, err := st.ResolveTargetHandle(t.Context(), "architect", "", now)
	if !errors.Is(err, ErrTargetHandleNotLive) {
		t.Fatalf("ResolveTargetHandle() error = %v, want ErrTargetHandleNotLive", err)
	}
	if resolved.StaleMatches != 1 {
		t.Fatalf("stale_matches = %d, want 1", resolved.StaleMatches)
	}
}

// A broad selector (no #) matching multiple live participants cannot be routed.
func TestResolveTargetHandleAmbiguous(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	registerHandleSession(t, st, "sess_a", "participant-a", "worker", "repo",
		now.Add(30*time.Minute), now.Add(-2*time.Minute))
	registerHandleSession(t, st, "sess_b", "participant-b", "worker", "repo",
		now.Add(30*time.Minute), now.Add(-1*time.Minute))

	_, err := st.ResolveTargetHandle(t.Context(), "worker", "repo", now)
	if !errors.Is(err, ErrTargetHandleAmbiguous) {
		t.Fatalf("ResolveTargetHandle() error = %v, want ErrTargetHandleAmbiguous", err)
	}
}

// A #-suffixed selector names one exact key: multiple live rows are never
// ambiguous (newest wins), and an unknown suffix fails CLOSED as not_found
// rather than broadening. WO-174.
func TestResolveTargetHandleSuffixedNeverAmbiguousAndFailsClosed(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	registerHandleSession(t, st, "sess_1", "participant-1", "worker/repo#abcd", "repo",
		now.Add(30*time.Minute), now.Add(-2*time.Minute))
	registerHandleSession(t, st, "sess_2", "participant-2", "worker/repo#abcd", "repo",
		now.Add(30*time.Minute), now.Add(-1*time.Minute))

	// Two live rows for the SAME suffixed handle -> newest wins, not ambiguous.
	resolved, err := st.ResolveTargetHandle(t.Context(), "worker/repo#abcd", "repo", now)
	if err != nil {
		t.Fatalf("ResolveTargetHandle(suffixed) error = %v, want newest-wins", err)
	}
	if resolved.ParticipantID != "participant-2" {
		t.Fatalf("resolved = %q, want participant-2 (freshest)", resolved.ParticipantID)
	}

	// Unknown suffix must fail closed, never degrade to broad role/repo match.
	if _, err := st.ResolveTargetHandle(t.Context(), "worker/repo#nope", "repo", now); !errors.Is(err, ErrTargetHandleNotFound) {
		t.Fatalf("unknown suffixed handle error = %v, want ErrTargetHandleNotFound (fail closed)", err)
	}
}

func TestResolveTargetHandleOrdersLastSeenChronologically(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 3, 12, 1, 0, 0, time.UTC)
	exactSecond := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	fractionalLater := exactSecond.Add(100 * time.Millisecond)

	// WO-181: RFC3339Nano text DESC sorts "...00Z" before "...00.1Z"; the
	// resolver must compare parsed times so newest-wins is chronological.
	registerHandleSession(t, st, "sess_exact", "participant-exact", "worker/repo#nanos", "repo",
		now.Add(30*time.Minute), exactSecond)
	registerHandleSession(t, st, "sess_fractional", "participant-fractional", "worker/repo#nanos", "repo",
		now.Add(30*time.Minute), fractionalLater)

	resolved, err := st.ResolveTargetHandle(t.Context(), "worker/repo#nanos", "repo", now)
	if err != nil {
		t.Fatalf("ResolveTargetHandle(RFC3339Nano ordering) error = %v", err)
	}
	if resolved.ParticipantID != "participant-fractional" {
		t.Fatalf("resolved = %q, want participant-fractional (chronologically newest)", resolved.ParticipantID)
	}
}

// A repository scope narrows resolution to the matching session.
func TestResolveTargetHandleScopesRepository(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	registerHandleSession(t, st, "sess_repo_a", "participant-repo-a", "architect", "repo-a",
		now.Add(30*time.Minute), now.Add(-2*time.Minute))
	registerHandleSession(t, st, "sess_repo_b", "participant-repo-b", "architect", "repo-b",
		now.Add(30*time.Minute), now.Add(-1*time.Minute))

	resolved, err := st.ResolveTargetHandle(t.Context(), "architect", "repo-b", now)
	if err != nil {
		t.Fatalf("ResolveTargetHandle(scoped) error = %v", err)
	}
	if resolved.ParticipantID != "participant-repo-b" {
		t.Fatalf("resolved = %q, want participant-repo-b", resolved.ParticipantID)
	}
}
