package model

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SecuritySchemeEd25519 identifies ed25519 signatures encoded with standard base64.
const SecuritySchemeEd25519 = "ed25519"

// CanonicalEnvelopeBytes returns the deterministic WO-87 signing payload for an envelope.
func CanonicalEnvelopeBytes(envelope Envelope) ([]byte, error) {
	unsigned := envelope
	unsigned.Security.Signature = ""

	return json.Marshal(unsigned)
}

// SignEnvelope signs an envelope using the ed25519 transport signature scheme.
func SignEnvelope(envelope Envelope, privateKey ed25519.PrivateKey) (Envelope, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Envelope{}, fmt.Errorf("ed25519 private key must be %d bytes", ed25519.PrivateKeySize)
	}
	if err := validateEd25519Envelope(envelope); err != nil {
		return Envelope{}, err
	}
	// WO-82: off-band envelopes are unsigned until this function attaches the signature.
	if err := envelope.validateForSigning(); err != nil {
		return Envelope{}, err
	}

	envelope.Security.Signed = true // WO-88: signed state is authenticated; only signature bytes are excluded.
	canonical, err := CanonicalEnvelopeBytes(envelope)
	if err != nil {
		return Envelope{}, err
	}

	signature := ed25519.Sign(privateKey, canonical)
	envelope.Security.Signature = base64.StdEncoding.EncodeToString(signature)

	return envelope, nil
}

// VerifyEnvelope verifies an envelope signed by SignEnvelope.
func VerifyEnvelope(envelope Envelope, publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	if err := validateEd25519Envelope(envelope); err != nil {
		return err
	}
	if !envelope.Security.Signed {
		return errors.New("envelope is not signed")
	}
	if strings.TrimSpace(envelope.Security.Signature) == "" {
		return errors.New("security.signature is required")
	}
	if err := envelope.Validate(); err != nil {
		return err
	}

	signature, err := base64.StdEncoding.DecodeString(envelope.Security.Signature)
	if err != nil {
		return fmt.Errorf("security.signature must be base64 ed25519 bytes: %w", err)
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("ed25519 signature must be %d bytes", ed25519.SignatureSize)
	}

	canonical, err := CanonicalEnvelopeBytes(envelope)
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, canonical, signature) {
		return errors.New("envelope signature is invalid")
	}

	return nil
}

func validateEd25519Envelope(envelope Envelope) error {
	if strings.TrimSpace(envelope.Security.Scheme) != SecuritySchemeEd25519 {
		return fmt.Errorf("security.scheme must be %q", SecuritySchemeEd25519)
	}

	return nil
}
