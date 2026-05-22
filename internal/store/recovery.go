package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

type promotedWorkOrderPayload struct {
	TrackingSystem      string           `json:"tracking_system"`
	WorkledgerProject   string           `json:"workledger_project"`
	WorkOrderID         int              `json:"work_order_id"`
	WorkOrderTitle      string           `json:"work_order_title"`
	SourceThreadID      string           `json:"source_thread_id"`
	OptionalSyncTargets []string         `json:"optional_sync_targets"`
	Confidence          model.Confidence `json:"confidence"`
	EvidenceIDs         []string         `json:"evidence_ids"`
}

type recoveryFactsPayload struct {
	RecoveryFacts []model.RecoveryFact `json:"recovery_facts"`
	Facts         []model.RecoveryFact `json:"facts"`
}

// BuildPromotedThreadRecoveryCapsule projects a thread into a compact recovery
// surface after promotion. It is intentionally derived from verified envelopes
// only and never includes raw transcript payloads.
func BuildPromotedThreadRecoveryCapsule(
	snapshot ThreadSnapshot,
	generatedAt time.Time,
) (model.PromotedThreadRecoveryCapsule, error) {
	if err := snapshot.Thread.Validate(); err != nil {
		return model.PromotedThreadRecoveryCapsule{}, fmt.Errorf("invalid thread: %w", err)
	}
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	}

	diagnosisEnvelope, diagnosis, err := latestVerifiedDiagnosis(snapshot.Thread.ThreadID, snapshot.Envelopes)
	if err != nil {
		return model.PromotedThreadRecoveryCapsule{}, err
	}

	promotionEnvelope, promotion, err := latestPromotionReceipt(snapshot.Thread.ThreadID, snapshot.Envelopes)
	if err != nil {
		return model.PromotedThreadRecoveryCapsule{}, err
	}

	evidence := recoveryEvidenceRefs(snapshot.Thread.Evidence, diagnosis.EvidenceIDs)
	capsule := model.PromotedThreadRecoveryCapsule{
		Type:         model.RecoveryCapsuleTypePromotedThread,
		ThreadID:     snapshot.Thread.ThreadID,
		ThreadTitle:  snapshot.Thread.Title,
		Status:       snapshot.Thread.Status,
		Source:       snapshot.Thread.Source,
		CustomerTier: snapshot.Thread.CustomerTier,
		GeneratedAt:  formatTime(generatedAt),
		VerifiedDiagnosis: model.DiagnosisRecoverySummary{
			Problem:             diagnosis.Problem,
			LikelyCause:         diagnosis.LikelyCause,
			ProposedRemediation: append([]string(nil), diagnosis.ProposedRemediation...),
			EvidenceIDs:         append([]string(nil), diagnosis.EvidenceIDs...),
			Confidence:          diagnosis.Confidence,
			Verified:            true,
			SourceMessageID:     diagnosisEnvelope.MessageID,
		},
		Evidence:              evidence,
		VerifiedFacts:         verifiedRecoveryFacts(diagnosisEnvelope, diagnosis, promotionEnvelope, promotion),
		RejectedOrStale:       rejectedOrStaleFacts(snapshot.Envelopes),
		MissingInfoResolution: missingInfoResolution(snapshot.Envelopes),
		Promotion: model.PromotionReceipt{
			TrackingSystem:      promotion.TrackingSystem,
			WorkledgerProject:   promotion.WorkledgerProject,
			WorkOrderID:         promotion.WorkOrderID,
			WorkOrderTitle:      promotion.WorkOrderTitle,
			SourceThreadID:      promotion.SourceThreadID,
			OptionalSyncTargets: append([]string(nil), promotion.OptionalSyncTargets...),
			Confidence:          promotion.Confidence,
			EvidenceIDs:         append([]string(nil), promotion.EvidenceIDs...),
			SourceMessageID:     promotionEnvelope.MessageID,
			PromotedAt:          formatTime(promotionEnvelope.SentAt),
		},
		NextRecommendedHandoff: nextRecoveryHandoff(snapshot.Thread.ThreadID, promotion),
	}

	return capsule, nil
}

func latestVerifiedDiagnosis(
	threadID string,
	envelopes []model.Envelope,
) (model.Envelope, model.Diagnosis, error) {
	var selectedEnvelope model.Envelope
	var selected model.Diagnosis
	found := false
	authoritativePairExists := hasSemanticallyValidAuthoritativePromotionPair(threadID, envelopes)

	for _, envelope := range envelopes {
		if envelope.Type != model.MessageTypeDiagnosisPropose ||
			!envelope.Trace.Verified ||
			!promotionPassedForRecovery(envelope, authoritativePairExists) {
			continue
		}

		diagnosis, err := recoveryDiagnosisFromEnvelope(envelope)
		if err != nil && envelope.Trace.PromotionStatus == model.PromotionStatusPassed {
			continue
		}
		if err != nil {
			return model.Envelope{}, model.Diagnosis{}, err
		}

		selectedEnvelope = envelope
		selected = diagnosis
		found = true
	}

	if !found {
		return model.Envelope{}, model.Diagnosis{}, errors.New("promoted recovery capsule requires a verified diagnosis")
	}

	return selectedEnvelope, selected, nil
}

func latestPromotionReceipt(
	threadID string,
	envelopes []model.Envelope,
) (model.Envelope, promotedWorkOrderPayload, error) {
	var selectedEnvelope model.Envelope
	var selected promotedWorkOrderPayload
	found := false
	authoritativePairExists := hasSemanticallyValidAuthoritativePromotionPair(threadID, envelopes)

	for _, envelope := range envelopes {
		if envelope.Type != model.MessageTypeWorkOrderCreate ||
			!envelope.Trace.Verified ||
			!promotionPassedForRecovery(envelope, authoritativePairExists) {
			continue
		}

		payload, err := recoveryPromotionReceiptFromEnvelope(
			threadID,
			envelope,
			envelope.Trace.PromotionStatus == "",
		)
		if err != nil && envelope.Trace.PromotionStatus == model.PromotionStatusPassed {
			continue
		}
		if err != nil {
			return model.Envelope{}, promotedWorkOrderPayload{}, err
		}

		selectedEnvelope = envelope
		selected = payload
		found = true
	}

	if !found {
		return model.Envelope{}, promotedWorkOrderPayload{}, errors.New("promoted recovery capsule requires a promotion receipt")
	}

	return selectedEnvelope, selected, nil
}

// WO-61: legacy fallback is bounded to all-legacy promoted threads only; once
// a thread has a semantically valid authoritative promotion-passed pair,
// empty-status promotion envelopes no longer qualify for recovery.
func promotionPassedForRecovery(envelope model.Envelope, authoritativePairExists bool) bool {
	switch envelope.Trace.PromotionStatus {
	case model.PromotionStatusPassed:
		return authoritativePairExists
	case "":
		return !authoritativePairExists
	default:
		return false
	}
}

func hasSemanticallyValidAuthoritativePromotionPair(threadID string, envelopes []model.Envelope) bool {
	hasDiagnosis := false
	hasPromotion := false

	for _, envelope := range envelopes {
		if !isAuthoritativePromotionEnvelope(envelope) {
			continue
		}
		switch envelope.Type {
		case model.MessageTypeDiagnosisPropose:
			if _, err := recoveryDiagnosisFromEnvelope(envelope); err == nil {
				hasDiagnosis = true
			}
		case model.MessageTypeWorkOrderCreate:
			if _, err := recoveryPromotionReceiptFromEnvelope(threadID, envelope, false); err == nil {
				hasPromotion = true
			}
		}
		if hasDiagnosis && hasPromotion {
			return true
		}
	}

	return false
}

func recoveryDiagnosisFromEnvelope(envelope model.Envelope) (model.Diagnosis, error) {
	var diagnosis model.Diagnosis
	if err := json.Unmarshal(envelope.Payload, &diagnosis); err != nil {
		return model.Diagnosis{}, fmt.Errorf("invalid verified diagnosis payload: %w", err)
	}
	if err := diagnosis.Validate(); err != nil {
		return model.Diagnosis{}, fmt.Errorf("invalid verified diagnosis: %w", err)
	}
	if !diagnosis.Verified {
		return model.Diagnosis{}, errors.New("verified diagnosis payload must carry verified=true")
	}
	if len(diagnosis.MissingInfo) > 0 {
		return model.Diagnosis{}, errors.New("promoted recovery capsule requires resolved missing_info")
	}

	return diagnosis, nil
}

func recoveryPromotionReceiptFromEnvelope(
	threadID string,
	envelope model.Envelope,
	allowLegacySourceDefault bool,
) (promotedWorkOrderPayload, error) {
	var payload promotedWorkOrderPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return promotedWorkOrderPayload{}, fmt.Errorf("invalid promotion receipt payload: %w", err)
	}
	if allowLegacySourceDefault && strings.TrimSpace(payload.SourceThreadID) == "" {
		payload.SourceThreadID = threadID
	}
	if strings.TrimSpace(payload.TrackingSystem) == "" {
		return promotedWorkOrderPayload{}, errors.New("promotion receipt tracking_system is required")
	}
	if payload.SourceThreadID != threadID {
		return promotedWorkOrderPayload{}, errors.New("promotion receipt source_thread_id does not match thread")
	}
	if envelope.Trace.PromotionStatus != model.PromotionStatusPassed {
		return payload, nil
	}
	if strings.TrimSpace(payload.TrackingSystem) != "workledger" {
		return promotedWorkOrderPayload{}, errors.New("promotion receipt tracking_system must be workledger")
	}
	if strings.TrimSpace(payload.WorkledgerProject) == "" {
		return promotedWorkOrderPayload{}, errors.New("promotion receipt workledger_project is required")
	}
	if payload.WorkOrderID <= 0 {
		return promotedWorkOrderPayload{}, errors.New("promotion receipt work_order_id must be positive")
	}
	if strings.TrimSpace(payload.WorkOrderTitle) == "" {
		return promotedWorkOrderPayload{}, errors.New("promotion receipt work_order_title is required")
	}

	return payload, nil
}

func recoveryEvidenceRefs(
	artifacts []model.Artifact,
	evidenceIDs []string,
) []model.RecoveryArtifactRef {
	byID := make(map[string]model.Artifact, len(artifacts))
	for _, artifact := range artifacts {
		byID[artifact.ArtifactID] = artifact
	}

	refs := make([]model.RecoveryArtifactRef, 0, len(evidenceIDs))
	for _, evidenceID := range evidenceIDs {
		evidenceID = strings.TrimSpace(evidenceID)
		if evidenceID == "" {
			continue
		}

		artifact, ok := byID[evidenceID]
		if !ok {
			refs = append(refs, model.RecoveryArtifactRef{ArtifactID: evidenceID})
			continue
		}

		refs = append(refs, model.RecoveryArtifactRef{
			ArtifactID:  artifact.ArtifactID,
			Name:        artifact.Name,
			Kind:        artifact.Kind,
			SHA256:      artifact.SHA256,
			SizeBytes:   artifact.SizeBytes,
			ContentType: artifact.ContentType,
			Redacted:    artifact.Redacted,
		})
	}

	return refs
}

func verifiedRecoveryFacts(
	envelope model.Envelope,
	diagnosis model.Diagnosis,
	promotionEnvelope model.Envelope,
	promotion promotedWorkOrderPayload,
) []model.RecoveryFact {
	observedAt := formatTime(envelope.SentAt)
	facts := []model.RecoveryFact{
		{
			State:           model.RecoveryFactVerified,
			Text:            "problem: " + diagnosis.Problem,
			SourceMessageID: envelope.MessageID,
			EvidenceIDs:     append([]string(nil), diagnosis.EvidenceIDs...),
			ObservedAt:      observedAt,
		},
		{
			State:           model.RecoveryFactVerified,
			Text:            "likely cause: " + diagnosis.LikelyCause,
			SourceMessageID: envelope.MessageID,
			EvidenceIDs:     append([]string(nil), diagnosis.EvidenceIDs...),
			ObservedAt:      observedAt,
		},
	}

	for _, step := range diagnosis.ProposedRemediation {
		facts = append(facts, model.RecoveryFact{
			State:           model.RecoveryFactVerified,
			Text:            "proposed remediation: " + step,
			SourceMessageID: envelope.MessageID,
			EvidenceIDs:     append([]string(nil), diagnosis.EvidenceIDs...),
			ObservedAt:      observedAt,
		})
	}

	workOrder := promotion.WorkOrderTitle
	if promotion.WorkOrderID > 0 && promotion.WorkledgerProject != "" {
		workOrder = fmt.Sprintf("%s/WO-%d: %s", promotion.WorkledgerProject, promotion.WorkOrderID, promotion.WorkOrderTitle)
	}
	if workOrder == "" {
		workOrder = promotion.TrackingSystem
	}
	facts = append(facts, model.RecoveryFact{
		State:           model.RecoveryFactPromotedToWO,
		Text:            "promoted to " + workOrder,
		SourceMessageID: promotionEnvelope.MessageID,
		EvidenceIDs:     append([]string(nil), promotion.EvidenceIDs...),
		ObservedAt:      formatTime(promotionEnvelope.SentAt),
	})

	return facts
}

func rejectedOrStaleFacts(envelopes []model.Envelope) []model.RecoveryFact {
	facts := []model.RecoveryFact{}
	for _, envelope := range envelopes {
		if !envelope.Trace.Verified {
			continue
		}

		var payload recoveryFactsPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			continue
		}
		candidates := append(payload.RecoveryFacts, payload.Facts...)
		for _, fact := range candidates {
			fact.Text = strings.TrimSpace(fact.Text)
			if fact.Text == "" {
				continue
			}
			if fact.State != model.RecoveryFactRejected &&
				fact.State != model.RecoveryFactSuperseded {
				continue
			}
			if fact.SourceMessageID == "" {
				fact.SourceMessageID = envelope.MessageID
			}
			if fact.ObservedAt == "" && !envelope.SentAt.IsZero() {
				fact.ObservedAt = formatTime(envelope.SentAt)
			}
			facts = append(facts, fact)
		}
	}

	return facts
}

func missingInfoResolution(envelopes []model.Envelope) model.MissingInfoResolution {
	notes := []string{"verified promoted diagnosis has no unresolved missing_info entries"}
	for _, envelope := range envelopes {
		if envelope.Type == model.MessageTypeClarifyResponse && envelope.Trace.Verified {
			notes = append(notes, "verified clarification response "+envelope.MessageID+" was recorded before promotion")
		}
	}

	return model.MissingInfoResolution{
		Status: "resolved",
		Notes:  notes,
	}
}

func nextRecoveryHandoff(threadID string, promotion promotedWorkOrderPayload) string {
	if promotion.WorkledgerProject != "" && promotion.WorkOrderID > 0 {
		return fmt.Sprintf(
			"use workledger %s/WO-%d as canonical execution source; keep Hivebus thread %s as provenance",
			promotion.WorkledgerProject,
			promotion.WorkOrderID,
			threadID,
		)
	}

	return fmt.Sprintf(
		"use the promoted %s receipt as the execution source; keep Hivebus thread %s as provenance",
		promotion.TrackingSystem,
		threadID,
	)
}
