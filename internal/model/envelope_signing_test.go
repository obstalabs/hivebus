package model

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSignEnvelopeRoundTripsSchemaMessageTypes(t *testing.T) {
	publicKey, privateKey := deterministicSigningKey()

	for _, envelope := range signedEnvelopeFixtures(t) {
		t.Run(string(envelope.Type), func(t *testing.T) {
			signed, err := SignEnvelope(envelope, privateKey)
			if err != nil {
				t.Fatalf("SignEnvelope() error = %v", err)
			}

			if !signed.Security.Signed {
				t.Fatal("SignEnvelope() did not mark the envelope signed")
			}
			if strings.TrimSpace(signed.Security.Signature) == "" {
				t.Fatal("SignEnvelope() did not attach a signature")
			}
			if err := VerifyEnvelope(signed, publicKey); err != nil {
				t.Fatalf("VerifyEnvelope() error = %v", err)
			}

			encoded, err := json.Marshal(signed)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			var decoded Envelope
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if err := VerifyEnvelope(decoded, publicKey); err != nil {
				t.Fatalf("VerifyEnvelope() after JSON round-trip error = %v", err)
			}
		})
	}
}

func TestVerifyEnvelopeRejectsTamperedReplyRoutingFields(t *testing.T) {
	publicKey, privateKey := deterministicSigningKey()
	envelope := signedEnvelopeFixtures(t)[2]

	signed, err := SignEnvelope(envelope, privateKey)
	if err != nil {
		t.Fatalf("SignEnvelope() error = %v", err)
	}

	signed.ReplyTo = "message-other"
	if err := VerifyEnvelope(signed, publicKey); err == nil {
		t.Fatal("VerifyEnvelope() expected a tamper error")
	}
}

func TestSignEnvelopeRejectsInvalidEnvelope(t *testing.T) {
	_, privateKey := deterministicSigningKey()
	envelope := signedEnvelopeFixtures(t)[0]
	envelope.ThreadID = ""

	if _, err := SignEnvelope(envelope, privateKey); err == nil {
		t.Fatal("SignEnvelope() expected validation error")
	}
}

func TestVerifyEnvelopeRejectsMalformedSignature(t *testing.T) {
	publicKey, privateKey := deterministicSigningKey()
	envelope := signedEnvelopeFixtures(t)[0]

	signed, err := SignEnvelope(envelope, privateKey)
	if err != nil {
		t.Fatalf("SignEnvelope() error = %v", err)
	}

	signed.Security.Signature = "not-base64"
	if err := VerifyEnvelope(signed, publicKey); err == nil {
		t.Fatal("VerifyEnvelope() expected malformed signature error")
	}
}

func TestCanonicalEnvelopeBytesIgnoreMutableSignatureFields(t *testing.T) {
	envelope := signedEnvelopeFixtures(t)[0]

	first, err := CanonicalEnvelopeBytes(envelope)
	if err != nil {
		t.Fatalf("CanonicalEnvelopeBytes() error = %v", err)
	}

	envelope.Security.Signed = true
	envelope.Security.Signature = "signature-written-after-canonicalization"
	second, err := CanonicalEnvelopeBytes(envelope)
	if err != nil {
		t.Fatalf("CanonicalEnvelopeBytes() error = %v", err)
	}

	if string(first) != string(second) {
		t.Fatal("CanonicalEnvelopeBytes() changed after mutable signature fields changed")
	}
}

func deterministicSigningKey() (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}

	privateKey := ed25519.NewKeyFromSeed(seed)
	return privateKey.Public().(ed25519.PublicKey), privateKey
}

func signedEnvelopeFixtures(t *testing.T) []Envelope {
	t.Helper()

	sentAt := time.Date(2026, time.June, 5, 9, 0, 0, 0, time.UTC)

	return []Envelope{
		newSignedEnvelopeFixture(
			MessageTypeQuery,
			"message-query",
			"",
			`{"question":"which checkout is canonical?","intent":"inspect_checkout"}`,
			sentAt,
		),
		newSignedEnvelopeFixture(
			MessageTypeAnswer,
			"message-answer",
			"message-query",
			`{"answer":"main at caa415d","evidence":["git status --short --branch"]}`,
			sentAt.Add(time.Second),
		),
		newSignedEnvelopeFixture(
			MessageTypeRequest,
			"message-request",
			"",
			`{"intent":"prioritize_blocker","authority":"receiver_disposes","requested_action":"review blocker"}`,
			sentAt.Add(2*time.Second),
		),
		newSignedEnvelopeFixture(
			MessageTypeAuthorityDirective,
			"message-authority",
			"message-request",
			`{"authority":"requires_operator","disposition":"operator_review_required"}`,
			sentAt.Add(3*time.Second),
		),
	}
}

func newSignedEnvelopeFixture(
	messageType MessageType,
	messageID string,
	replyTo string,
	payload string,
	sentAt time.Time,
) Envelope {
	return Envelope{
		MessageID:      messageID,
		ThreadID:       "thread-signed-envelope",
		From:           "architect/agent",
		To:             []string{"workledger/agent"},
		Type:           messageType,
		Payload:        json.RawMessage(payload),
		ReplyTo:        replyTo,
		SentAt:         sentAt,
		IdempotencyKey: messageID + "-idem",
		Trace: Trace{
			CorrelationID:   "corr-signed-envelope",
			Verified:        true,
			PromotionStatus: PromotionStatusPassed,
		},
		Security: Security{
			Scheme: SecuritySchemeEd25519,
			Nonce:  messageID + "-nonce",
		},
	}
}
