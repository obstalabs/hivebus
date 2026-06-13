package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
)

const (
	capabilityValidationSuccessThreshold = 3
	capabilityDecayWindow                = 30 * 24 * time.Hour
)

var (
	ErrDiscoveredCapabilityNotFound = errors.New("discovered capability not found")
	nonCapabilitySlugPattern        = regexp.MustCompile(`[^a-z0-9]+`)
)

type capabilityProfile struct {
	ID           string
	Requirements []string
	Risk         model.CapabilityRiskLevel
}

func (s *Store) ListDiscoveredCapabilities(
	ctx context.Context,
	workerID string,
	now time.Time,
) ([]model.DiscoveredCapability, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store is not initialized")
	}
	if strings.TrimSpace(workerID) == "" {
		return nil, errors.New("worker_id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			worker_id,
			capability_id,
			requirements_text,
			risk_level,
			trust_level,
			observation_count,
			success_count,
			first_observed_at,
			last_observed_at,
			approved_by,
			approved_at,
			last_observed_tool
		FROM worker_capabilities
		WHERE worker_id = ?
		ORDER BY capability_id ASC
	`, workerID)
	if err != nil {
		return nil, fmt.Errorf("query discovered capabilities: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	records := make([]model.DiscoveredCapability, 0)
	for rows.Next() {
		record, err := scanDiscoveredCapability(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, withDerivedCapabilityState(record, now))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate discovered capabilities: %w", err)
	}

	return records, nil
}

func (s *Store) ApproveDiscoveredCapability(
	ctx context.Context,
	workerID string,
	capabilityID string,
	approvedBy string,
	now time.Time,
) (model.DiscoveredCapability, error) {
	if s == nil || s.db == nil {
		return model.DiscoveredCapability{}, errors.New("store is not initialized")
	}
	if strings.TrimSpace(workerID) == "" {
		return model.DiscoveredCapability{}, errors.New("worker_id is required")
	}
	if strings.TrimSpace(capabilityID) == "" {
		return model.DiscoveredCapability{}, errors.New("capability_id is required")
	}
	if strings.TrimSpace(approvedBy) == "" {
		return model.DiscoveredCapability{}, errors.New("approved_by is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE worker_capabilities
		SET trust_level = ?, approved_by = ?, approved_at = ?
		WHERE worker_id = ? AND capability_id = ?
	`,
		string(model.CapabilityTrustTrusted),
		approvedBy,
		formatTime(now),
		workerID,
		capabilityID,
	)
	if err != nil {
		return model.DiscoveredCapability{}, fmt.Errorf("approve discovered capability: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return model.DiscoveredCapability{}, fmt.Errorf("inspect approved capability update: %w", err)
	}
	if rowsAffected == 0 {
		return model.DiscoveredCapability{}, ErrDiscoveredCapabilityNotFound
	}

	records, err := s.ListDiscoveredCapabilities(ctx, workerID, now)
	if err != nil {
		return model.DiscoveredCapability{}, err
	}
	for _, record := range records {
		if record.CapabilityID == capabilityID {
			return record, nil
		}
	}

	return model.DiscoveredCapability{}, ErrDiscoveredCapabilityNotFound
}

func trustedCapabilitiesTx(
	ctx context.Context,
	tx *sql.Tx,
	workerID string,
	now time.Time,
) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT capability_id, last_observed_at
		FROM worker_capabilities
		WHERE worker_id = ? AND trust_level = ?
	`, workerID, string(model.CapabilityTrustTrusted))
	if err != nil {
		return nil, fmt.Errorf("query trusted capabilities: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var trusted []string
	for rows.Next() {
		var capabilityID string
		var lastObservedAt string
		if err := rows.Scan(&capabilityID, &lastObservedAt); err != nil {
			return nil, fmt.Errorf("scan trusted capability: %w", err)
		}
		if capabilityDecayed(parseTime(lastObservedAt), now) {
			continue
		}
		trusted = append(trusted, capabilityID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trusted capabilities: %w", err)
	}

	return trusted, nil
}

func recordCapabilityObservationsTx(
	ctx context.Context,
	tx *sql.Tx,
	lease Lease,
	result model.Envelope,
	now time.Time,
) error {
	payload, err := model.ParseTaskResultFinalPayload(result.Payload)
	if err != nil {
		return nil
	}

	tools := payload.NormalizedTools()
	if len(tools) == 0 {
		return nil
	}
	success := payload.Successful()

	for _, tool := range tools {
		profile := capabilityProfileForTool(tool)
		inserted, err := insertCapabilityObservationTx(ctx, tx, lease, result, profile, tool, success, now)
		if err != nil {
			return err
		}
		if !inserted {
			continue
		}
		if err := upsertDiscoveredCapabilityTx(ctx, tx, lease.WorkerID, profile, tool, success, now); err != nil {
			return err
		}
	}

	return nil
}

func insertCapabilityObservationTx(
	ctx context.Context,
	tx *sql.Tx,
	lease Lease,
	result model.Envelope,
	profile capabilityProfile,
	tool string,
	success bool,
	now time.Time,
) (bool, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO capability_observations (
			worker_id,
			thread_id,
			lease_id,
			task_message_id,
			result_message_id,
			capability_id,
			tool_name,
			success,
			observed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		lease.WorkerID,
		lease.ThreadID,
		lease.LeaseID,
		lease.TaskMessageID,
		result.MessageID,
		profile.ID,
		tool,
		boolToInt(success),
		formatTime(now),
	)
	if err != nil {
		return false, fmt.Errorf("insert capability observation: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect capability observation insert: %w", err)
	}
	return rowsAffected > 0, nil
}

func upsertDiscoveredCapabilityTx(
	ctx context.Context,
	tx *sql.Tx,
	workerID string,
	profile capabilityProfile,
	tool string,
	success bool,
	now time.Time,
) error {
	existing, err := loadDiscoveredCapabilityTx(ctx, tx, workerID, profile.ID)
	if err != nil && !errors.Is(err, ErrDiscoveredCapabilityNotFound) {
		return err
	}

	switch {
	case errors.Is(err, ErrDiscoveredCapabilityNotFound):
		record := model.DiscoveredCapability{
			WorkerID:         workerID,
			CapabilityID:     profile.ID,
			Requirements:     profile.Requirements,
			RiskLevel:        profile.Risk,
			TrustLevel:       model.CapabilityTrustObserved,
			ObservationCount: 1,
			SuccessCount:     boolToInt(success),
			FirstObservedAt:  now.UTC(),
			LastObservedAt:   now.UTC(),
			LastObservedTool: tool,
		}
		if record.SuccessCount >= capabilityValidationSuccessThreshold {
			record.TrustLevel = model.CapabilityTrustValidated
		}
		if err := insertDiscoveredCapabilityTx(ctx, tx, record); err != nil {
			return err
		}
		return nil
	default:
		existing.ObservationCount++
		if success {
			existing.SuccessCount++
		}
		existing.LastObservedAt = now.UTC()
		existing.LastObservedTool = tool
		if existing.TrustLevel != model.CapabilityTrustTrusted &&
			existing.SuccessCount >= capabilityValidationSuccessThreshold {
			existing.TrustLevel = model.CapabilityTrustValidated
		}
		if err := updateDiscoveredCapabilityTx(ctx, tx, existing); err != nil {
			return err
		}
		return nil
	}
}

func insertDiscoveredCapabilityTx(
	ctx context.Context,
	tx *sql.Tx,
	record model.DiscoveredCapability,
) error {
	if err := record.Validate(); err != nil {
		return err
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO worker_capabilities (
			worker_id,
			capability_id,
			requirements_text,
			risk_level,
			trust_level,
			observation_count,
			success_count,
			first_observed_at,
			last_observed_at,
			approved_by,
			approved_at,
			last_observed_tool
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		record.WorkerID,
		record.CapabilityID,
		joinCapabilities(record.Requirements),
		string(record.RiskLevel),
		string(record.TrustLevel),
		record.ObservationCount,
		record.SuccessCount,
		formatTime(record.FirstObservedAt),
		formatTime(record.LastObservedAt),
		record.ApprovedBy,
		optionalTimeString(record.ApprovedAt),
		record.LastObservedTool,
	)
	if err != nil {
		return fmt.Errorf("insert discovered capability: %w", err)
	}
	return nil
}

func updateDiscoveredCapabilityTx(
	ctx context.Context,
	tx *sql.Tx,
	record model.DiscoveredCapability,
) error {
	if err := record.Validate(); err != nil {
		return err
	}

	_, err := tx.ExecContext(ctx, `
		UPDATE worker_capabilities
		SET requirements_text = ?,
			risk_level = ?,
			trust_level = ?,
			observation_count = ?,
			success_count = ?,
			first_observed_at = ?,
			last_observed_at = ?,
			approved_by = ?,
			approved_at = ?,
			last_observed_tool = ?
		WHERE worker_id = ? AND capability_id = ?
	`,
		joinCapabilities(record.Requirements),
		string(record.RiskLevel),
		string(record.TrustLevel),
		record.ObservationCount,
		record.SuccessCount,
		formatTime(record.FirstObservedAt),
		formatTime(record.LastObservedAt),
		record.ApprovedBy,
		optionalTimeString(record.ApprovedAt),
		record.LastObservedTool,
		record.WorkerID,
		record.CapabilityID,
	)
	if err != nil {
		return fmt.Errorf("update discovered capability: %w", err)
	}
	return nil
}

func loadDiscoveredCapabilityTx(
	ctx context.Context,
	tx *sql.Tx,
	workerID string,
	capabilityID string,
) (model.DiscoveredCapability, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT
			worker_id,
			capability_id,
			requirements_text,
			risk_level,
			trust_level,
			observation_count,
			success_count,
			first_observed_at,
			last_observed_at,
			approved_by,
			approved_at,
			last_observed_tool
		FROM worker_capabilities
		WHERE worker_id = ? AND capability_id = ?
	`, workerID, capabilityID)

	record, err := scanDiscoveredCapability(row)
	if err != nil {
		return model.DiscoveredCapability{}, err
	}
	return record, nil
}

func scanDiscoveredCapability(scanner interface {
	Scan(dest ...any) error
}) (model.DiscoveredCapability, error) {
	var record model.DiscoveredCapability
	var requirementsText string
	var riskLevel string
	var trustLevel string
	var firstObservedAt string
	var lastObservedAt string
	var approvedAt string
	if err := scanner.Scan(
		&record.WorkerID,
		&record.CapabilityID,
		&requirementsText,
		&riskLevel,
		&trustLevel,
		&record.ObservationCount,
		&record.SuccessCount,
		&firstObservedAt,
		&lastObservedAt,
		&record.ApprovedBy,
		&approvedAt,
		&record.LastObservedTool,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.DiscoveredCapability{}, ErrDiscoveredCapabilityNotFound
		}
		return model.DiscoveredCapability{}, fmt.Errorf("scan discovered capability: %w", err)
	}

	record.Requirements = splitCapabilities(requirementsText)
	record.RiskLevel = model.CapabilityRiskLevel(riskLevel)
	record.TrustLevel = model.CapabilityTrustLevel(trustLevel)
	record.FirstObservedAt = parseTime(firstObservedAt)
	record.LastObservedAt = parseTime(lastObservedAt)
	if parsedApprovedAt := parseTime(approvedAt); !parsedApprovedAt.IsZero() {
		record.ApprovedAt = &parsedApprovedAt
	}

	return record, nil
}

func withDerivedCapabilityState(
	record model.DiscoveredCapability,
	now time.Time,
) model.DiscoveredCapability {
	record.PendingApproval = record.TrustLevel == model.CapabilityTrustValidated &&
		strings.TrimSpace(record.ApprovedBy) == ""
	record.Decayed = capabilityDecayed(record.LastObservedAt, now)
	return record
}

func capabilityDecayed(lastObservedAt time.Time, now time.Time) bool {
	if lastObservedAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return lastObservedAt.Before(now.Add(-capabilityDecayWindow))
}

func effectiveCapabilities(explicit []string, trusted []string) []string {
	seen := make(map[string]struct{}, len(explicit)+len(trusted))
	merged := make([]string, 0, len(explicit)+len(trusted))
	appendUnique := func(values []string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			merged = append(merged, value)
		}
	}
	appendUnique(explicit)
	appendUnique(trusted)
	sort.Strings(merged)
	return merged
}

func capabilityProfileForTool(tool string) capabilityProfile {
	normalized := normalizeCapabilitySlug(tool)
	switch normalized {
	case "go-test", "gotest", "go-testing":
		return capabilityProfile{
			ID:           "go-testing",
			Requirements: []string{"go", "fs"},
			Risk:         model.CapabilityRiskLow,
		}
	case "go", "go-build":
		return capabilityProfile{
			ID:           "go",
			Requirements: []string{"go"},
			Risk:         model.CapabilityRiskLow,
		}
	case "docker", "docker-build":
		return capabilityProfile{
			ID:           "docker",
			Requirements: []string{"docker", "fs"},
			Risk:         model.CapabilityRiskMedium,
		}
	case "bash", "sh", "zsh", "shell":
		return capabilityProfile{
			ID:           "shell",
			Requirements: []string{"fs"},
			Risk:         model.CapabilityRiskMedium,
		}
	case "pytest", "python-test", "python-testing":
		return capabilityProfile{
			ID:           "python-testing",
			Requirements: []string{"python", "fs"},
			Risk:         model.CapabilityRiskLow,
		}
	default:
		if normalized == "" {
			normalized = "unknown"
		}
		return capabilityProfile{
			ID:           normalized,
			Requirements: []string{normalized},
			Risk:         model.CapabilityRiskMedium,
		}
	}
}

func normalizeCapabilitySlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = nonCapabilitySlugPattern.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	return value
}

func optionalTimeString(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return formatTime(*value)
}
