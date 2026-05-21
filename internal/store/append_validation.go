package store

import (
	"errors"

	"github.com/ppiankov/hivebus/internal/model"
)

// WO-64: direct append surfaces must not mint legacy or promotion-passed truth.
func ValidateThreadAppendEnvelope(envelope model.Envelope) error {
	if !envelope.Trace.Verified || !isPromotionRecoveryEnvelopeType(envelope.Type) {
		return nil
	}

	switch envelope.Trace.PromotionStatus {
	case "":
		return errors.New("verified promotion diagnosis/work_order envelopes with empty trace.promotion_status are reserved for legacy replay only")
	case model.PromotionStatusPassed:
		return errors.New("verified promotion-passed diagnosis/work_order envelopes must be created via FinalizePromotion")
	default:
		return nil
	}
}

func isAuthoritativePromotionEnvelope(envelope model.Envelope) bool {
	if !isPromotionRecoveryEnvelopeType(envelope.Type) {
		return false
	}
	if !envelope.Trace.Verified {
		return false
	}
	return envelope.Trace.PromotionStatus == model.PromotionStatusPassed
}

func isPromotionRecoveryEnvelopeType(messageType model.MessageType) bool {
	return messageType == model.MessageTypeDiagnosisPropose ||
		messageType == model.MessageTypeWorkOrderCreate
}
