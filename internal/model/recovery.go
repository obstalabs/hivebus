package model

// RecoveryFactState labels whether a recovery fact is usable, stale, or only
// useful as provenance. Recovery capsules must never rely on unlabeled history.
type RecoveryFactState string

const (
	RecoveryCapsuleTypePromotedThread = "promoted_thread_recovery_capsule"

	RecoveryFactVerified          RecoveryFactState = "verified"
	RecoveryFactRejected          RecoveryFactState = "rejected"
	RecoveryFactSuperseded        RecoveryFactState = "superseded"
	RecoveryFactPromotedToWO      RecoveryFactState = "promoted_to_wo"
	RecoveryFactOperatorConfirmed RecoveryFactState = "operator_confirmed"
)

// RecoveryFact is a compact labeled fact safe for handoff after compaction.
type RecoveryFact struct {
	State           RecoveryFactState `json:"state"`
	Text            string            `json:"text"`
	SourceMessageID string            `json:"source_message_id,omitempty"`
	EvidenceIDs     []string          `json:"evidence_ids,omitempty"`
	ObservedAt      string            `json:"observed_at,omitempty"`
}

// DiagnosisRecoverySummary carries only the verified diagnosis, not the
// investigator transcript that produced it.
type DiagnosisRecoverySummary struct {
	Problem             string     `json:"problem"`
	LikelyCause         string     `json:"likely_cause"`
	ProposedRemediation []string   `json:"proposed_remediation"`
	EvidenceIDs         []string   `json:"evidence_ids"`
	Confidence          Confidence `json:"confidence"`
	Verified            bool       `json:"verified"`
	SourceMessageID     string     `json:"source_message_id"`
}

// RecoveryArtifactRef identifies evidence without inlining artifact contents.
type RecoveryArtifactRef struct {
	ArtifactID  string `json:"artifact_id"`
	Name        string `json:"name,omitempty"`
	Kind        string `json:"kind,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Redacted    bool   `json:"redacted,omitempty"`
}

// MissingInfoResolution records that promotion happened only after missing
// information was resolved, without replaying clarification transcript text.
type MissingInfoResolution struct {
	Status string   `json:"status"`
	Notes  []string `json:"notes,omitempty"`
}

// PromotionReceipt is the local receipt that the verified diagnosis became a
// canonical work order. It is a stored fact, not a live dependency on Workledger.
type PromotionReceipt struct {
	TrackingSystem      string     `json:"tracking_system"`
	WorkledgerProject   string     `json:"workledger_project,omitempty"`
	WorkOrderID         int        `json:"work_order_id,omitempty"`
	WorkOrderTitle      string     `json:"work_order_title,omitempty"`
	SourceThreadID      string     `json:"source_thread_id"`
	OptionalSyncTargets []string   `json:"optional_sync_targets,omitempty"`
	Confidence          Confidence `json:"confidence"`
	EvidenceIDs         []string   `json:"evidence_ids"`
	SourceMessageID     string     `json:"source_message_id"`
	PromotedAt          string     `json:"promoted_at"`
}

// PromotedThreadRecoveryCapsule is the post-compaction handoff surface for a
// promoted Hivebus thread. It deliberately excludes raw envelopes and narration.
type PromotedThreadRecoveryCapsule struct {
	Type                   string                   `json:"type"`
	ThreadID               string                   `json:"thread_id"`
	ThreadTitle            string                   `json:"thread_title"`
	Status                 ThreadStatus             `json:"status"`
	Source                 string                   `json:"source"`
	CustomerTier           Tier                     `json:"customer_tier"`
	GeneratedAt            string                   `json:"generated_at"`
	VerifiedDiagnosis      DiagnosisRecoverySummary `json:"verified_diagnosis"`
	Evidence               []RecoveryArtifactRef    `json:"evidence"`
	VerifiedFacts          []RecoveryFact           `json:"verified_facts"`
	RejectedOrStale        []RecoveryFact           `json:"rejected_or_stale,omitempty"`
	MissingInfoResolution  MissingInfoResolution    `json:"missing_info_resolution"`
	Promotion              PromotionReceipt         `json:"promotion"`
	NextRecommendedHandoff string                   `json:"next_recommended_handoff"`
}
