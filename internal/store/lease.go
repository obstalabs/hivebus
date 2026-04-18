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
	eventKindWorkerLease = "worker.lease"
)

var (
	ErrTaskNotFound       = errors.New("task not found")
	ErrTaskAlreadyClaimed = errors.New("task already claimed")
	ErrTaskCompleted      = errors.New("task already completed")
	ErrWorkerBusy         = errors.New("worker already has an active lease")
	ErrLeaseNotFound      = errors.New("lease not found")
	ErrLeaseExpired       = errors.New("lease expired")
	ErrLeaseFinalized     = errors.New("lease already finalized")
	ErrLeaseNotOwned      = errors.New("lease is owned by another worker")
)

type LeaseStatus string

const (
	LeaseStatusActive    LeaseStatus = "active"
	LeaseStatusCompleted LeaseStatus = "completed"
	LeaseStatusExpired   LeaseStatus = "expired"
)

type LeaseReceiptAction string

const (
	LeaseReceiptClaimed   LeaseReceiptAction = "claimed"
	LeaseReceiptRenewed   LeaseReceiptAction = "renewed"
	LeaseReceiptCompleted LeaseReceiptAction = "completed"
	LeaseReceiptExpired   LeaseReceiptAction = "expired"
)

type Lease struct {
	LeaseID           string      `json:"lease_id"`
	ThreadID          string      `json:"thread_id"`
	TaskMessageID     string      `json:"task_message_id"`
	AcceptedMessageID string      `json:"accepted_message_id"`
	WorkerID          string      `json:"worker_id"`
	ClaimedAt         time.Time   `json:"claimed_at"`
	LeasedUntil       time.Time   `json:"leased_until"`
	Status            LeaseStatus `json:"status"`
	ResultMessageID   string      `json:"result_message_id,omitempty"`
	CompletedAt       *time.Time  `json:"completed_at,omitempty"`
}

type LeaseReceipt struct {
	LeaseID           string             `json:"lease_id"`
	ThreadID          string             `json:"thread_id"`
	TaskMessageID     string             `json:"task_message_id"`
	AcceptedMessageID string             `json:"accepted_message_id,omitempty"`
	ResultMessageID   string             `json:"result_message_id,omitempty"`
	WorkerID          string             `json:"worker_id"`
	Action            LeaseReceiptAction `json:"action"`
	At                time.Time          `json:"at"`
	LeasedUntil       *time.Time         `json:"leased_until,omitempty"`
}

type WorkerTask struct {
	ThreadID string         `json:"thread_id"`
	Envelope model.Envelope `json:"envelope"`
}

type PollResultStatus string

const (
	PollResultIdle      PollResultStatus = "idle"
	PollResultAvailable PollResultStatus = "available"
	PollResultBusy      PollResultStatus = "busy"
)

type PollResult struct {
	Status PollResultStatus `json:"status"`
	Task   *WorkerTask      `json:"task,omitempty"`
	Lease  *Lease           `json:"lease,omitempty"`
}

func (s *Store) PollTask(
	ctx context.Context,
	workerID string,
	capabilities []string,
	now time.Time,
) (PollResult, error) {
	if strings.TrimSpace(workerID) == "" {
		return PollResult{}, errors.New("worker_id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PollResult{}, fmt.Errorf("begin poll transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := expireStaleLeasesTx(ctx, tx, now); err != nil {
		return PollResult{}, err
	}

	activeLease, err := findActiveLeaseByWorkerTx(ctx, tx, workerID)
	if err != nil {
		return PollResult{}, err
	}
	if activeLease != nil {
		if err := tx.Commit(); err != nil {
			return PollResult{}, fmt.Errorf("commit busy poll: %w", err)
		}
		return PollResult{
			Status: PollResultBusy,
			Lease:  activeLease,
		}, nil
	}

	trusted, err := trustedCapabilitiesTx(ctx, tx, workerID, now)
	if err != nil {
		return PollResult{}, err
	}
	task, err := findAvailableTaskTx(ctx, tx, workerID, effectiveCapabilities(capabilities, trusted))
	if err != nil {
		return PollResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PollResult{}, fmt.Errorf("commit poll: %w", err)
	}
	if task == nil {
		return PollResult{Status: PollResultIdle}, nil
	}

	return PollResult{
		Status: PollResultAvailable,
		Task:   task,
	}, nil
}

func (s *Store) ClaimTask(
	ctx context.Context,
	workerID string,
	taskMessageID string,
	accepted model.Envelope,
	leaseDuration time.Duration,
	now time.Time,
) (Lease, error) {
	if strings.TrimSpace(workerID) == "" {
		return Lease{}, errors.New("worker_id is required")
	}
	if strings.TrimSpace(taskMessageID) == "" {
		return Lease{}, errors.New("task_message_id is required")
	}
	if leaseDuration <= 0 {
		return Lease{}, errors.New("lease duration must be positive")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Lease{}, fmt.Errorf("begin claim transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := expireStaleLeasesTx(ctx, tx, now); err != nil {
		return Lease{}, err
	}

	if activeLease, err := findActiveLeaseByWorkerTx(ctx, tx, workerID); err != nil {
		return Lease{}, err
	} else if activeLease != nil {
		return Lease{}, ErrWorkerBusy
	}

	request, err := loadTaskEnvelopeTx(ctx, tx, taskMessageID)
	if err != nil {
		return Lease{}, err
	}
	if err := accepted.ValidateTaskAccepted(request); err != nil {
		return Lease{}, err
	}
	if accepted.From != workerID {
		return Lease{}, errors.New("task.accepted from must match worker_id")
	}

	if activeLease, completed, err := taskLeaseStateTx(ctx, tx, taskMessageID); err != nil {
		return Lease{}, err
	} else if completed {
		return Lease{}, ErrTaskCompleted
	} else if activeLease != nil {
		return Lease{}, ErrTaskAlreadyClaimed
	}

	lease := Lease{
		LeaseID:           accepted.MessageID,
		ThreadID:          request.ThreadID,
		TaskMessageID:     request.MessageID,
		AcceptedMessageID: accepted.MessageID,
		WorkerID:          workerID,
		ClaimedAt:         now.UTC(),
		LeasedUntil:       now.UTC().Add(leaseDuration),
		Status:            LeaseStatusActive,
	}

	if err := insertEnvelopeEventTx(ctx, tx, accepted); err != nil {
		return Lease{}, err
	}
	if err := insertLeaseTx(ctx, tx, lease); err != nil {
		return Lease{}, err
	}
	if err := appendLeaseReceiptTx(ctx, tx, LeaseReceipt{
		LeaseID:           lease.LeaseID,
		ThreadID:          lease.ThreadID,
		TaskMessageID:     lease.TaskMessageID,
		AcceptedMessageID: lease.AcceptedMessageID,
		WorkerID:          lease.WorkerID,
		Action:            LeaseReceiptClaimed,
		At:                now.UTC(),
		LeasedUntil:       pointerTime(lease.LeasedUntil),
	}); err != nil {
		return Lease{}, err
	}

	if err := tx.Commit(); err != nil {
		return Lease{}, fmt.Errorf("commit claim: %w", err)
	}

	return lease, nil
}

func (s *Store) RenewLease(
	ctx context.Context,
	leaseID string,
	workerID string,
	leaseDuration time.Duration,
	now time.Time,
) (Lease, error) {
	if strings.TrimSpace(leaseID) == "" {
		return Lease{}, errors.New("lease_id is required")
	}
	if strings.TrimSpace(workerID) == "" {
		return Lease{}, errors.New("worker_id is required")
	}
	if leaseDuration <= 0 {
		return Lease{}, errors.New("lease duration must be positive")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Lease{}, fmt.Errorf("begin renew transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := expireStaleLeasesTx(ctx, tx, now); err != nil {
		return Lease{}, err
	}

	lease, err := loadLeaseTx(ctx, tx, leaseID)
	if err != nil {
		return Lease{}, err
	}
	if lease.WorkerID != workerID {
		return Lease{}, ErrLeaseNotOwned
	}
	switch lease.Status {
	case LeaseStatusExpired:
		return Lease{}, ErrLeaseExpired
	case LeaseStatusCompleted:
		return Lease{}, ErrLeaseFinalized
	case LeaseStatusActive:
	default:
		return Lease{}, fmt.Errorf("unsupported lease status %q", lease.Status)
	}

	lease.LeasedUntil = now.UTC().Add(leaseDuration)
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE worker_leases SET leased_until = ? WHERE lease_id = ?`,
		formatTime(lease.LeasedUntil),
		lease.LeaseID,
	); err != nil {
		return Lease{}, fmt.Errorf("renew lease: %w", err)
	}
	if err := appendLeaseReceiptTx(ctx, tx, LeaseReceipt{
		LeaseID:           lease.LeaseID,
		ThreadID:          lease.ThreadID,
		TaskMessageID:     lease.TaskMessageID,
		AcceptedMessageID: lease.AcceptedMessageID,
		WorkerID:          lease.WorkerID,
		Action:            LeaseReceiptRenewed,
		At:                now.UTC(),
		LeasedUntil:       pointerTime(lease.LeasedUntil),
	}); err != nil {
		return Lease{}, err
	}

	if err := tx.Commit(); err != nil {
		return Lease{}, fmt.Errorf("commit renew: %w", err)
	}

	return lease, nil
}

func (s *Store) CompleteLease(
	ctx context.Context,
	leaseID string,
	workerID string,
	result model.Envelope,
	now time.Time,
) (Lease, error) {
	if strings.TrimSpace(leaseID) == "" {
		return Lease{}, errors.New("lease_id is required")
	}
	if strings.TrimSpace(workerID) == "" {
		return Lease{}, errors.New("worker_id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Lease{}, fmt.Errorf("begin complete transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := expireStaleLeasesTx(ctx, tx, now); err != nil {
		return Lease{}, err
	}

	lease, err := loadLeaseTx(ctx, tx, leaseID)
	if err != nil {
		return Lease{}, err
	}
	if lease.WorkerID != workerID {
		return Lease{}, ErrLeaseNotOwned
	}
	switch lease.Status {
	case LeaseStatusExpired:
		return Lease{}, ErrLeaseExpired
	case LeaseStatusCompleted:
		return Lease{}, ErrLeaseFinalized
	case LeaseStatusActive:
	default:
		return Lease{}, fmt.Errorf("unsupported lease status %q", lease.Status)
	}

	request, err := loadTaskEnvelopeTx(ctx, tx, lease.TaskMessageID)
	if err != nil {
		return Lease{}, err
	}
	snapshot, err := loadThreadSnapshotTx(ctx, tx, lease.ThreadID)
	if err != nil {
		return Lease{}, err
	}
	pendingClarification, err := snapshot.Thread.HasPendingClarification(
		lease.TaskMessageID,
		snapshot.Envelopes,
		now,
	)
	if err != nil {
		return Lease{}, err
	}
	if pendingClarification {
		return Lease{}, ErrClarificationPending
	}
	if err := result.ValidateTaskResultFinal(request); err != nil {
		return Lease{}, err
	}
	if result.From != workerID {
		return Lease{}, errors.New("task.result.final from must match worker_id")
	}

	completedAt := now.UTC()
	lease.Status = LeaseStatusCompleted
	lease.CompletedAt = &completedAt
	lease.ResultMessageID = result.MessageID

	if err := insertEnvelopeEventTx(ctx, tx, result); err != nil {
		return Lease{}, err
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE worker_leases
		 SET status = ?, completed_at = ?, result_message_id = ?
		 WHERE lease_id = ?`,
		string(lease.Status),
		formatTime(completedAt),
		lease.ResultMessageID,
		lease.LeaseID,
	); err != nil {
		return Lease{}, fmt.Errorf("complete lease: %w", err)
	}
	if err := appendLeaseReceiptTx(ctx, tx, LeaseReceipt{
		LeaseID:           lease.LeaseID,
		ThreadID:          lease.ThreadID,
		TaskMessageID:     lease.TaskMessageID,
		AcceptedMessageID: lease.AcceptedMessageID,
		ResultMessageID:   lease.ResultMessageID,
		WorkerID:          lease.WorkerID,
		Action:            LeaseReceiptCompleted,
		At:                completedAt,
	}); err != nil {
		return Lease{}, err
	}
	if err := recordCapabilityObservationsTx(ctx, tx, lease, result, completedAt); err != nil {
		return Lease{}, err
	}

	if err := tx.Commit(); err != nil {
		return Lease{}, fmt.Errorf("commit complete: %w", err)
	}

	return lease, nil
}

func (s *Store) AppendLeaseResultPart(
	ctx context.Context,
	leaseID string,
	workerID string,
	result model.Envelope,
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
		return fmt.Errorf("begin partial-result transaction: %w", err)
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

	request, err := loadTaskEnvelopeTx(ctx, tx, lease.TaskMessageID)
	if err != nil {
		return err
	}
	if err := result.ValidateTaskResultPart(request); err != nil {
		return err
	}
	if result.From != workerID {
		return errors.New("task.result.partial from must match worker_id")
	}

	if err := insertEnvelopeEventTx(ctx, tx, result); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit partial result: %w", err)
	}

	return nil
}

func expireStaleLeasesTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT lease_id, thread_id, task_message_id, accepted_message_id, worker_id, claimed_at, leased_until, status, result_message_id, completed_at
		 FROM worker_leases
		 WHERE status = ? AND leased_until < ?
		 ORDER BY leased_until ASC`,
		string(LeaseStatusActive),
		formatTime(now.UTC()),
	)
	if err != nil {
		return fmt.Errorf("query stale leases: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var staleLeases []Lease
	for rows.Next() {
		lease, err := scanLease(rows)
		if err != nil {
			return err
		}
		staleLeases = append(staleLeases, lease)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate stale leases: %w", err)
	}

	for _, lease := range staleLeases {
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE worker_leases SET status = ? WHERE lease_id = ?`,
			string(LeaseStatusExpired),
			lease.LeaseID,
		); err != nil {
			return fmt.Errorf("expire lease %s: %w", lease.LeaseID, err)
		}

		expiredAt := now.UTC()
		if err := appendLeaseReceiptTx(ctx, tx, LeaseReceipt{
			LeaseID:           lease.LeaseID,
			ThreadID:          lease.ThreadID,
			TaskMessageID:     lease.TaskMessageID,
			AcceptedMessageID: lease.AcceptedMessageID,
			WorkerID:          lease.WorkerID,
			Action:            LeaseReceiptExpired,
			At:                expiredAt,
		}); err != nil {
			return err
		}
	}

	return nil
}

func findActiveLeaseByWorkerTx(ctx context.Context, tx *sql.Tx, workerID string) (*Lease, error) {
	row := tx.QueryRowContext(
		ctx,
		`SELECT lease_id, thread_id, task_message_id, accepted_message_id, worker_id, claimed_at, leased_until, status, result_message_id, completed_at
		 FROM worker_leases
		 WHERE worker_id = ? AND status = ?
		 LIMIT 1`,
		workerID,
		string(LeaseStatusActive),
	)

	lease, err := scanLease(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return &lease, nil
}

func findAvailableTaskTx(
	ctx context.Context,
	tx *sql.Tx,
	workerID string,
	capabilities []string,
) (*WorkerTask, error) {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT message_id, thread_id, payload_json
		 FROM thread_events
		 WHERE event_kind = ?
		 ORDER BY sequence ASC`,
		eventKindEnvelopeAppended,
	)
	if err != nil {
		return nil, fmt.Errorf("query candidate tasks: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		var messageID string
		var threadID string
		var payload []byte
		if err := rows.Scan(&messageID, &threadID, &payload); err != nil {
			return nil, fmt.Errorf("scan candidate task: %w", err)
		}

		var envelope model.Envelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, fmt.Errorf("unmarshal candidate task: %w", err)
		}
		if envelope.Type != model.MessageTypeTaskRequest {
			continue
		}
		if err := envelope.ValidateTaskRequest(); err != nil {
			return nil, err
		}
		if !matchesWorker(envelope, workerID, capabilities) {
			continue
		}

		activeLease, completed, err := taskLeaseStateTx(ctx, tx, envelope.MessageID)
		if err != nil {
			return nil, err
		}
		if completed || activeLease != nil {
			continue
		}

		return &WorkerTask{
			ThreadID: threadID,
			Envelope: envelope,
		}, nil
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate candidate tasks: %w", err)
	}

	return nil, nil
}

func matchesWorker(envelope model.Envelope, workerID string, capabilities []string) bool {
	if contains(envelope.To, workerID) {
		return true
	}
	if len(envelope.To) > 0 {
		return false
	}
	if strings.TrimSpace(envelope.Capability) == "" {
		return false
	}

	return contains(capabilities, envelope.Capability)
}

func taskLeaseStateTx(ctx context.Context, tx *sql.Tx, taskMessageID string) (*Lease, bool, error) {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT lease_id, thread_id, task_message_id, accepted_message_id, worker_id, claimed_at, leased_until, status, result_message_id, completed_at
		 FROM worker_leases
		 WHERE task_message_id = ?
		 ORDER BY claimed_at DESC`,
		taskMessageID,
	)
	if err != nil {
		return nil, false, fmt.Errorf("query task lease state: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		lease, err := scanLease(rows)
		if err != nil {
			return nil, false, err
		}

		switch lease.Status {
		case LeaseStatusCompleted:
			return nil, true, nil
		case LeaseStatusActive:
			return &lease, false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate task lease state: %w", err)
	}

	return nil, false, nil
}

func loadTaskEnvelopeTx(ctx context.Context, tx *sql.Tx, messageID string) (model.Envelope, error) {
	row := tx.QueryRowContext(
		ctx,
		`SELECT payload_json
		 FROM thread_events
		 WHERE event_kind = ? AND message_id = ?
		 LIMIT 1`,
		eventKindEnvelopeAppended,
		messageID,
	)

	var payload []byte
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Envelope{}, ErrTaskNotFound
		}
		return model.Envelope{}, fmt.Errorf("query task envelope: %w", err)
	}

	var envelope model.Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return model.Envelope{}, fmt.Errorf("unmarshal task envelope: %w", err)
	}
	if err := envelope.ValidateTaskRequest(); err != nil {
		return model.Envelope{}, err
	}

	return envelope, nil
}

func insertLeaseTx(ctx context.Context, tx *sql.Tx, lease Lease) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO worker_leases (
			lease_id, thread_id, task_message_id, accepted_message_id, worker_id, claimed_at, leased_until, status, result_message_id, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		lease.LeaseID,
		lease.ThreadID,
		lease.TaskMessageID,
		lease.AcceptedMessageID,
		lease.WorkerID,
		formatTime(lease.ClaimedAt),
		formatTime(lease.LeasedUntil),
		string(lease.Status),
		nullString(lease.ResultMessageID),
		nullTime(lease.CompletedAt),
	)
	if err != nil {
		return mapLeaseInsertError(err)
	}

	return nil
}

func appendLeaseReceiptTx(ctx context.Context, tx *sql.Tx, receipt LeaseReceipt) error {
	payload, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("marshal lease receipt: %w", err)
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO thread_events (thread_id, event_kind, event_at, payload_json)
		 VALUES (?, ?, ?, ?)`,
		receipt.ThreadID,
		eventKindWorkerLease,
		formatTime(receipt.At),
		payload,
	)
	if err != nil {
		return fmt.Errorf("insert lease receipt event: %w", err)
	}

	return nil
}

func loadLeaseTx(ctx context.Context, tx *sql.Tx, leaseID string) (Lease, error) {
	row := tx.QueryRowContext(
		ctx,
		`SELECT lease_id, thread_id, task_message_id, accepted_message_id, worker_id, claimed_at, leased_until, status, result_message_id, completed_at
		 FROM worker_leases
		 WHERE lease_id = ?
		 LIMIT 1`,
		leaseID,
	)

	lease, err := scanLease(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Lease{}, ErrLeaseNotFound
		}
		return Lease{}, err
	}

	return lease, nil
}

func scanLease(scanner interface{ Scan(dest ...any) error }) (Lease, error) {
	var lease Lease
	var claimedAt string
	var leasedUntil string
	var status string
	var resultMessageID sql.NullString
	var completedAt sql.NullString

	if err := scanner.Scan(
		&lease.LeaseID,
		&lease.ThreadID,
		&lease.TaskMessageID,
		&lease.AcceptedMessageID,
		&lease.WorkerID,
		&claimedAt,
		&leasedUntil,
		&status,
		&resultMessageID,
		&completedAt,
	); err != nil {
		return Lease{}, err
	}

	lease.ClaimedAt = parseTime(claimedAt)
	lease.LeasedUntil = parseTime(leasedUntil)
	lease.Status = LeaseStatus(status)
	lease.ResultMessageID = resultMessageID.String
	if completedAt.Valid {
		completed := parseTime(completedAt.String)
		lease.CompletedAt = &completed
	}

	return lease, nil
}

func mapLeaseInsertError(err error) error {
	if err == nil {
		return nil
	}

	message := err.Error()
	switch {
	case strings.Contains(message, "worker_leases_active_task_unique"):
		return ErrTaskAlreadyClaimed
	case strings.Contains(message, "worker_leases_active_worker_unique"):
		return ErrWorkerBusy
	case strings.Contains(message, "worker_leases_completed_task_unique"):
		return ErrTaskCompleted
	default:
		return err
	}
}

func insertEnvelopeEventTx(ctx context.Context, tx *sql.Tx, envelope model.Envelope) error {
	if err := envelope.Validate(); err != nil {
		return err
	}

	payload, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO thread_events (thread_id, event_kind, message_id, idempotency_key, event_at, payload_json)
		 VALUES (?, ?, ?, ?, ?, ?)`,
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

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}

	return parsed
}

func nullString(value string) sql.NullString {
	if strings.TrimSpace(value) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func nullTime(value *time.Time) sql.NullString {
	if value == nil || value.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(value.UTC()), Valid: true}
}

func pointerTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
