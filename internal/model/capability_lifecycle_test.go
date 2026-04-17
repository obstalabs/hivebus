package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCapabilityLifecyclePayloadValidateAcceptsVerifiedInstall(t *testing.T) {
	t.Helper()

	payload := sampleCapabilityLifecyclePayload()
	payload.AttestationState = CapabilityAttestationVerified
	payload.ApprovedBy = "operator.pavel"

	if err := payload.Validate(MessageTypeInstallVerified); err != nil {
		t.Fatalf("Validate(install verified) error = %v", err)
	}
}

func TestCapabilityLifecyclePayloadValidateSeparatesTaskOutcomeFromAttestation(t *testing.T) {
	t.Helper()

	payload := sampleCapabilityLifecyclePayload()
	payload.AttestationState = CapabilityAttestationActive
	payload.ApprovedBy = "operator.pavel"
	payload.TaskOutcome = CapabilityTaskSucceeded

	if err := payload.Validate(MessageTypeTaskCompleted); err != nil {
		t.Fatalf("Validate(task completed) error = %v", err)
	}
}

func TestCapabilityLifecyclePayloadValidateRejectsMissingFailureReason(t *testing.T) {
	t.Helper()

	payload := sampleCapabilityLifecyclePayload()
	payload.AttestationState = CapabilityAttestationDoctorFailed
	payload.ApprovedBy = "operator.pavel"

	if err := payload.Validate(MessageTypeDoctorFailed); err == nil {
		t.Fatal("Validate(doctor failed) expected an error")
	}
}

func TestCapabilityLifecyclePayloadValidateRejectsMissingOrigin(t *testing.T) {
	t.Helper()

	payload := sampleCapabilityLifecyclePayload()
	payload.AttestationState = CapabilityAttestationRequested
	payload.OriginThreadID = ""
	payload.OriginWorkOrderID = ""

	if err := payload.Validate(MessageTypeInstallRequested); err == nil {
		t.Fatal("Validate(install requested) expected an error")
	}
}

func TestCapabilityLifecyclePayloadValidateRejectsMissingArtifactReference(t *testing.T) {
	t.Helper()

	payload := sampleCapabilityLifecyclePayload()
	payload.AttestationState = CapabilityAttestationRequested
	payload.ArtifactRef = ""

	if err := payload.Validate(MessageTypeInstallRequested); err == nil {
		t.Fatal("Validate(install requested) expected an error")
	}
}

func TestEnvelopeValidateCapabilityLifecycleAcceptsTaskCompleted(t *testing.T) {
	t.Helper()

	payload := sampleCapabilityLifecyclePayload()
	payload.AttestationState = CapabilityAttestationActive
	payload.ApprovedBy = "operator.pavel"
	payload.TaskOutcome = CapabilityTaskSucceeded
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	envelope := Envelope{
		MessageID:      "msg_cap_005",
		ThreadID:       "thr_capability_smokevm",
		From:           "service.edge-installer",
		To:             []string{"service.sentinel"},
		Type:           MessageTypeTaskCompleted,
		Payload:        body,
		ArtifactIDs:    []string{"art_capability_receipt"},
		SentAt:         time.Date(2026, 4, 17, 6, 4, 0, 0, time.UTC),
		IdempotencyKey: "idem_msg_cap_005",
		Trace: Trace{
			CorrelationID: "corr_thr_capability_smokevm",
		},
		Security: Security{
			Scheme: "ed25519",
			Nonce:  "nonce_msg_cap_005",
		},
	}

	if err := envelope.ValidateCapabilityLifecycle(); err != nil {
		t.Fatalf("ValidateCapabilityLifecycle() error = %v", err)
	}
}

func sampleCapabilityLifecyclePayload() CapabilityLifecyclePayload {
	return CapabilityLifecyclePayload{
		Host:            "smokevm-arm64",
		CapabilityID:    "cap_go_test_arm64",
		CapabilityClass: "go-testing",
		Authorization: AuthorizationContext{
			SenderParticipantID:   "collector.nullbot",
			ParticipantMembership: ParticipantMembershipThreadParticipant,
			RequestedScope:        "capability.install.go-testing",
			RequestClass:          AuthorizationRequestCapability,
			ApprovalState:         AuthorizationApproved,
			ExpiresAt:             "2026-04-18T06:00:00Z",
		},
		Version:           "1.22.3",
		Digest:            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ArtifactRef:       "artifact://sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AttestationRef:    "attestation://sig/cap_go_test_arm64",
		Signer:            "buildkite-release",
		TrustRoot:         "obstalabs-release-root",
		RequestedBy:       "collector.nullbot",
		OriginThreadID:    "thr_smoke_arm64",
		OriginWorkOrderID: "WO-167",
		ExpiresAt:         "2026-04-18T06:00:00Z",
		DeliveryMode:      CapabilityDeliveryReference,
		EvidenceIDs:       []string{"art_capability_receipt"},
	}
}
