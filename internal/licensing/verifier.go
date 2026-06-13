package licensing

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/obstalabs/hivebus/internal/model"
)

const (
	// VerifyKeyEnv is the shared Obstalabs license verification key env var.
	VerifyKeyEnv = "OL_LICENSE_VERIFY_KEY"

	// LicensePrefix is the shared Obstalabs license key prefix.
	LicensePrefix = "ol_"

	// ProductName is the product entitlement name Hivebus requires.
	ProductName = "hivebus"

	licensePayloadVersion = 2
	licenseClockSkew      = 5 * time.Minute
)

// LicenseTier is the commercial tier string carried by billing v2 licenses.
type LicenseTier string

const (
	LicenseTierFree       LicenseTier = "free"
	LicenseTierTrial      LicenseTier = "trial"
	LicenseTierPro        LicenseTier = "pro"
	LicenseTierTeam       LicenseTier = "team"
	LicenseTierTeams      LicenseTier = "teams"
	LicenseTierEnterprise LicenseTier = "enterprise"
)

// ProductEntitlement is one product+tier grant in a unified Obstalabs license.
type ProductEntitlement struct {
	Product string      `json:"product"`
	Tier    LicenseTier `json:"tier"`
}

// Payload is the signed claim set embedded in a unified billing v2 license key.
type Payload struct {
	Version        int                  `json:"version"`
	LicenseID      string               `json:"license_id"`
	Subject        string               `json:"subject"`
	Products       []string             `json:"products"`
	Entitlements   []ProductEntitlement `json:"entitlements"`
	Tier           string               `json:"tier,omitempty"`
	Plan           string               `json:"plan,omitempty"`
	Email          string               `json:"email,omitempty"`
	SubscriptionID string               `json:"subscription_id,omitempty"`
	IssuedAt       int64                `json:"issued_at"`
	NotBefore      int64                `json:"not_before"`
	ExpiresAt      int64                `json:"expires_at"`
	Issuer         string               `json:"issuer"`
	KeyID          string               `json:"key_id"`
}

// VerifiedLicense is the Hivebus entitlement extracted from a valid license.
type VerifiedLicense struct {
	Payload     Payload
	Entitlement ProductEntitlement
}

// Verifier checks unified Obstalabs ol_ license keys.
type Verifier struct {
	publicKey ed25519.PublicKey
}

// NewVerifier creates a verifier from a base64-encoded Ed25519 public key.
func NewVerifier(encodedPublicKey string) (*Verifier, error) {
	publicKey, err := parseVerifyKey(encodedPublicKey)
	if err != nil {
		return nil, err
	}

	return &Verifier{publicKey: publicKey}, nil
}

// NewVerifierFromEnv creates a verifier using OL_LICENSE_VERIFY_KEY.
func NewVerifierFromEnv(getenv func(string) string) (*Verifier, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	return NewVerifier(getenv(VerifyKeyEnv))
}

// VerifyHivebus verifies a license and returns its Hivebus product entitlement.
func (v *Verifier) VerifyHivebus(rawKey string) (VerifiedLicense, error) {
	return v.VerifyHivebusAt(rawKey, time.Now().UTC())
}

// VerifyHivebusAt verifies a license at a deterministic time for tests.
func (v *Verifier) VerifyHivebusAt(rawKey string, now time.Time) (VerifiedLicense, error) {
	if v == nil || len(v.publicKey) == 0 {
		return VerifiedLicense{}, fmt.Errorf("license verifier is not configured")
	}

	encodedPayload, signature, err := splitLicenseKey(rawKey)
	if err != nil {
		return VerifiedLicense{}, err
	}

	// Billing signs the encoded payload string, not the decoded JSON bytes.
	if !ed25519.Verify(v.publicKey, []byte(encodedPayload), signature) {
		return VerifiedLicense{}, fmt.Errorf("license signature verification failed")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return VerifiedLicense{}, fmt.Errorf("decode license payload: %w", err)
	}

	var payload Payload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return VerifiedLicense{}, fmt.Errorf("parse license payload: %w", err)
	}

	entitlement, err := payload.hivebusEntitlement(now)
	if err != nil {
		return VerifiedLicense{}, err
	}

	return VerifiedLicense{Payload: payload, Entitlement: entitlement}, nil
}

func splitLicenseKey(rawKey string) (string, []byte, error) {
	key := strings.TrimSpace(rawKey)
	if !strings.HasPrefix(key, LicensePrefix) {
		return "", nil, fmt.Errorf("license key must use %s prefix", LicensePrefix)
	}

	parts := strings.SplitN(strings.TrimPrefix(key, LicensePrefix), ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", nil, fmt.Errorf("license key must contain payload and signature")
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", nil, fmt.Errorf("decode license signature: %w", err)
	}
	if len(signature) != ed25519.SignatureSize {
		return "", nil, fmt.Errorf("license signature must decode to %d bytes", ed25519.SignatureSize)
	}

	return parts[0], signature, nil
}

func (payload Payload) hivebusEntitlement(now time.Time) (ProductEntitlement, error) {
	if err := payload.validateShape(); err != nil {
		return ProductEntitlement{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if now.Add(licenseClockSkew).Before(time.Unix(payload.NotBefore, 0).UTC()) {
		return ProductEntitlement{}, fmt.Errorf("license is not valid yet")
	}
	if now.After(time.Unix(payload.ExpiresAt, 0).UTC().Add(licenseClockSkew)) {
		return ProductEntitlement{}, fmt.Errorf("license is expired")
	}

	for _, entitlement := range payload.Entitlements {
		if strings.TrimSpace(entitlement.Product) != ProductName {
			continue
		}
		if err := validateTier(entitlement.Tier); err != nil {
			return ProductEntitlement{}, err
		}
		return entitlement, nil
	}

	return ProductEntitlement{}, fmt.Errorf("license does not include %s entitlement", ProductName)
}

func (payload Payload) validateShape() error {
	if payload.Version != licensePayloadVersion {
		return fmt.Errorf("unsupported license payload version %d", payload.Version)
	}
	if payload.LicenseID == "" || payload.Subject == "" || payload.Issuer == "" || payload.KeyID == "" {
		return fmt.Errorf("license payload missing required identity fields")
	}
	if payload.IssuedAt == 0 {
		return fmt.Errorf("license payload missing issued_at")
	}
	if payload.NotBefore == 0 || payload.ExpiresAt == 0 || payload.ExpiresAt < payload.NotBefore {
		return fmt.Errorf("license payload has invalid validity window")
	}
	return nil
}

func validateTier(tier LicenseTier) error {
	switch tier {
	case LicenseTierFree, LicenseTierTrial, LicenseTierPro, LicenseTierTeam, LicenseTierTeams, LicenseTierEnterprise:
		return nil
	default:
		return fmt.Errorf("license has unsupported hivebus tier %q", tier)
	}
}

// CustomerTier maps a billing license tier into the local policy tier space.
func (entitlement ProductEntitlement) CustomerTier() (model.Tier, bool) {
	switch entitlement.Tier {
	case LicenseTierFree:
		return model.TierFree, true
	case LicenseTierTrial, LicenseTierPro:
		return model.TierPro, true
	case LicenseTierTeam, LicenseTierTeams:
		return model.TierTeams, true
	case LicenseTierEnterprise:
		return model.TierEnterprise, true
	default:
		return "", false
	}
}

func parseVerifyKey(value string) (ed25519.PublicKey, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("%s is required", VerifyKeyEnv)
	}

	for _, encoding := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		publicKey, err := encoding.DecodeString(trimmed)
		if err != nil {
			continue
		}
		if len(publicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%s must decode to %d bytes", VerifyKeyEnv, ed25519.PublicKeySize)
		}
		return ed25519.PublicKey(publicKey), nil
	}

	return nil, fmt.Errorf("%s must be base64 encoded", VerifyKeyEnv)
}
