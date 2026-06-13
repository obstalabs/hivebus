package policy

import (
	"fmt"

	"github.com/obstalabs/hivebus/internal/model"
)

const mib = 1024 * 1024

// Limits capture the commercial boundaries for each edition without changing protocol shape.
type Limits struct {
	MaxRecipients     int   `json:"max_recipients"`
	MaxArtifactCount  int   `json:"max_artifact_count"`
	MaxArtifactBytes  int64 `json:"max_artifact_bytes"`
	RetentionDays     int   `json:"retention_days"`
	RequireSigned     bool  `json:"require_signed"`
	RequireAuditTrace bool  `json:"require_audit_trace"`
}

var limitsByTier = map[model.Tier]Limits{
	model.TierFree: {
		MaxRecipients:     4,
		MaxArtifactCount:  8,
		MaxArtifactBytes:  8 * mib,
		RetentionDays:     7,
		RequireSigned:     true,
		RequireAuditTrace: true,
	},
	model.TierPro: {
		MaxRecipients:     16,
		MaxArtifactCount:  16,
		MaxArtifactBytes:  64 * mib,
		RetentionDays:     30,
		RequireSigned:     true,
		RequireAuditTrace: true,
	},
	model.TierTeams: {
		MaxRecipients:     48,
		MaxArtifactCount:  32,
		MaxArtifactBytes:  256 * mib,
		RetentionDays:     90,
		RequireSigned:     true,
		RequireAuditTrace: true,
	},
	model.TierEnterprise: {
		MaxRecipients:     128,
		MaxArtifactCount:  64,
		MaxArtifactBytes:  1024 * mib,
		RetentionDays:     365,
		RequireSigned:     true,
		RequireAuditTrace: true,
	},
}

// LimitsFor returns the edition policy for a given customer tier.
func LimitsFor(tier model.Tier) Limits {
	return limitsByTier[tier]
}

// ValidateEnvelope combines structural validation with tier-specific safety bounds.
func ValidateEnvelope(tier model.Tier, envelope model.Envelope, artifacts []model.Artifact) error {
	if err := envelope.Validate(); err != nil {
		return err
	}

	limits, ok := limitsByTier[tier]
	if !ok {
		return fmt.Errorf("unsupported tier %q", tier)
	}

	if len(envelope.To) > limits.MaxRecipients {
		return fmt.Errorf("recipient count %d exceeds tier limit %d", len(envelope.To), limits.MaxRecipients)
	}

	if len(artifacts) > limits.MaxArtifactCount {
		return fmt.Errorf("artifact count %d exceeds tier limit %d", len(artifacts), limits.MaxArtifactCount)
	}

	var totalBytes int64
	for _, artifact := range artifacts {
		if err := artifact.Validate(); err != nil {
			return err
		}

		totalBytes += artifact.SizeBytes
	}

	if totalBytes > limits.MaxArtifactBytes {
		return fmt.Errorf("artifact bytes %d exceed tier limit %d", totalBytes, limits.MaxArtifactBytes)
	}

	if limits.RequireSigned && !envelope.Security.Signed {
		return fmt.Errorf("tier %q requires signed envelopes", tier)
	}

	if limits.RequireAuditTrace && !envelope.Trace.Verified {
		return fmt.Errorf("tier %q requires verified trace metadata", tier)
	}

	return nil
}
