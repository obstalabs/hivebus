package licensing

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/model"
)

func TestVerifyHivebusAcceptsPeriodBoundV2PaidLicense(t *testing.T) {
	privateKey := testPrivateKey(1)
	verifier := mustVerifier(t, privateKey)

	now := time.Unix(1_800_000_000, 0).UTC()
	payload := validPayload(now, LicenseTierTeams)
	payload.Products = []string{"verdict", ProductName}
	payload.Entitlements = []ProductEntitlement{
		{Product: "verdict", Tier: LicenseTierPro},
		{Product: ProductName, Tier: LicenseTierTeams},
	}
	key := signLicense(t, privateKey, payload)

	verified, err := verifier.VerifyHivebusAt(key, now)
	if err != nil {
		t.Fatalf("verify hivebus license: %v", err)
	}
	if verified.Entitlement.Product != ProductName {
		t.Fatalf("expected hivebus entitlement, got %q", verified.Entitlement.Product)
	}
	if verified.Entitlement.Tier != LicenseTierTeams {
		t.Fatalf("expected teams tier, got %q", verified.Entitlement.Tier)
	}
	if verified.Payload.Email != "operator@example.com" {
		t.Fatalf("expected payload email to round-trip, got %q", verified.Payload.Email)
	}
	if verified.Payload.SubscriptionID != "sub_123" {
		t.Fatalf("expected subscription id to round-trip, got %q", verified.Payload.SubscriptionID)
	}
	customerTier, ok := verified.Entitlement.CustomerTier()
	if !ok || customerTier != model.TierTeams {
		t.Fatalf("customer tier = %q/%v, want teams/true", customerTier, ok)
	}
}

func TestVerifyHivebusAcceptsPeriodBoundV2TrialLicense(t *testing.T) {
	privateKey := testPrivateKey(2)
	verifier := mustVerifier(t, privateKey)

	now := time.Unix(1_800_000_000, 0).UTC()
	key := signLicense(t, privateKey, validPayload(now, LicenseTierTrial))

	verified, err := verifier.VerifyHivebusAt(key, now)
	if err != nil {
		t.Fatalf("verify trial license: %v", err)
	}
	if verified.Entitlement.Tier != LicenseTierTrial {
		t.Fatalf("expected trial tier, got %q", verified.Entitlement.Tier)
	}
	customerTier, ok := verified.Entitlement.CustomerTier()
	if !ok || customerTier != model.TierPro {
		t.Fatalf("customer tier = %q/%v, want pro/true", customerTier, ok)
	}
}

func TestVerifyHivebusUsesVerifyKeyEnv(t *testing.T) {
	privateKey := testPrivateKey(3)
	publicKey := privateKey.Public().(ed25519.PublicKey)

	verifier, err := NewVerifierFromEnv(func(name string) string {
		if name != VerifyKeyEnv {
			t.Fatalf("unexpected env var %q", name)
		}
		return base64.StdEncoding.EncodeToString(publicKey)
	})
	if err != nil {
		t.Fatalf("new verifier from env: %v", err)
	}

	key := signLicense(t, privateKey, validPayload(time.Now().UTC(), LicenseTierPro))
	if _, err := verifier.VerifyHivebus(key); err != nil {
		t.Fatalf("verify hivebus license: %v", err)
	}
}

func TestVerifyHivebusRejectsLicenseWithoutHivebusEntitlement(t *testing.T) {
	privateKey := testPrivateKey(4)
	verifier := mustVerifier(t, privateKey)

	now := time.Unix(1_800_000_000, 0).UTC()
	payload := validPayload(now, LicenseTierPro)
	payload.Products = []string{"verdict"}
	payload.Entitlements = []ProductEntitlement{{Product: "verdict", Tier: LicenseTierPro}}
	key := signLicense(t, privateKey, payload)

	if _, err := verifier.VerifyHivebusAt(key, now); err == nil {
		t.Fatal("expected missing hivebus entitlement to be rejected")
	}
}

func TestVerifyHivebusRejectsProductListWithoutExplicitEntitlement(t *testing.T) {
	privateKey := testPrivateKey(5)
	verifier := mustVerifier(t, privateKey)

	now := time.Unix(1_800_000_000, 0).UTC()
	payload := validPayload(now, LicenseTierPro)
	payload.Products = []string{ProductName}
	payload.Entitlements = nil
	key := signLicense(t, privateKey, payload)

	if _, err := verifier.VerifyHivebusAt(key, now); err == nil {
		t.Fatal("expected product list without hivebus entitlement to be rejected")
	}
}

func TestVerifyHivebusRejectsOldHivebusPrefix(t *testing.T) {
	privateKey := testPrivateKey(6)
	verifier := mustVerifier(t, privateKey)

	key := signLicense(t, privateKey, validPayload(time.Now().UTC(), LicenseTierPro))
	oldKey := "hb_" + strings.TrimPrefix(key, LicensePrefix)

	if _, err := verifier.VerifyHivebus(oldKey); err == nil {
		t.Fatal("expected old hb_ key to be rejected")
	}
}

func TestVerifyHivebusRejectsOldPayloadShape(t *testing.T) {
	privateKey := testPrivateKey(7)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	verifier := mustVerifier(t, privateKey)

	oldPayload := map[string]any{
		"products": []map[string]string{{"p": ProductName, "t": string(LicenseTierPro)}},
		"e":        "operator@example.com",
		"x":        time.Now().UTC().Add(time.Hour).Unix(),
		"s":        "sub_old",
	}
	data, err := json.Marshal(oldPayload)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	signature := base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(encoded)))
	key := LicensePrefix + encoded + "." + signature

	if _, err := verifier.VerifyHivebus(key); err == nil {
		t.Fatal("expected old payload shape to be rejected")
	}
	if _, err := NewVerifier(base64.RawURLEncoding.EncodeToString(publicKey)); err != nil {
		t.Fatalf("expected verify key to remain usable: %v", err)
	}
}

func TestVerifyHivebusRejectsTamperedSignature(t *testing.T) {
	privateKey := testPrivateKey(8)
	verifier := mustVerifier(t, privateKey)

	key := signLicense(t, privateKey, validPayload(time.Now().UTC(), LicenseTierPro))
	tampered := key[:len(key)-1] + differentBase64URLChar(key[len(key)-1])

	if _, err := verifier.VerifyHivebus(tampered); err == nil {
		t.Fatal("expected tampered signature to be rejected")
	}
}

func TestVerifyHivebusRejectsExpiredLicense(t *testing.T) {
	privateKey := testPrivateKey(9)
	verifier := mustVerifier(t, privateKey)

	now := time.Unix(1_800_000_000, 0).UTC()
	payload := validPayload(now, LicenseTierPro)
	payload.NotBefore = now.Add(-2 * time.Hour).Unix()
	payload.ExpiresAt = now.Add(-licenseClockSkew - time.Second).Unix()
	key := signLicense(t, privateKey, payload)

	if _, err := verifier.VerifyHivebusAt(key, now); err == nil {
		t.Fatal("expected expired license to be rejected")
	}
}

func TestVerifyHivebusRejectsFutureNotBefore(t *testing.T) {
	privateKey := testPrivateKey(10)
	verifier := mustVerifier(t, privateKey)

	now := time.Unix(1_800_000_000, 0).UTC()
	payload := validPayload(now, LicenseTierPro)
	payload.NotBefore = now.Add(licenseClockSkew + time.Second).Unix()
	payload.ExpiresAt = now.Add(time.Hour).Unix()
	key := signLicense(t, privateKey, payload)

	if _, err := verifier.VerifyHivebusAt(key, now); err == nil {
		t.Fatal("expected future not_before license to be rejected")
	}
}

func TestVerifyHivebusAcceptsClockSkewBoundaries(t *testing.T) {
	privateKey := testPrivateKey(11)
	verifier := mustVerifier(t, privateKey)

	now := time.Unix(1_800_000_000, 0).UTC()

	notBeforePayload := validPayload(now, LicenseTierPro)
	notBeforePayload.NotBefore = now.Add(licenseClockSkew).Unix()
	notBeforeKey := signLicense(t, privateKey, notBeforePayload)
	if _, err := verifier.VerifyHivebusAt(notBeforeKey, now); err != nil {
		t.Fatalf("expected not_before boundary to validate: %v", err)
	}

	expiryPayload := validPayload(now, LicenseTierPro)
	expiryPayload.NotBefore = now.Add(-time.Hour).Unix()
	expiryPayload.ExpiresAt = now.Add(-licenseClockSkew).Unix()
	expiryKey := signLicense(t, privateKey, expiryPayload)
	if _, err := verifier.VerifyHivebusAt(expiryKey, now); err != nil {
		t.Fatalf("expected expiry boundary to validate: %v", err)
	}
}

func TestVerifyHivebusRejectsUnsupportedTier(t *testing.T) {
	privateKey := testPrivateKey(12)
	verifier := mustVerifier(t, privateKey)

	now := time.Unix(1_800_000_000, 0).UTC()
	key := signLicense(t, privateKey, validPayload(now, LicenseTier("starter")))

	if _, err := verifier.VerifyHivebusAt(key, now); err == nil {
		t.Fatal("expected unsupported tier to be rejected")
	}
}

func validPayload(now time.Time, tier LicenseTier) Payload {
	return Payload{
		Version:        licensePayloadVersion,
		LicenseID:      "lic_test",
		Subject:        "cus_test",
		Products:       []string{ProductName},
		Entitlements:   []ProductEntitlement{{Product: ProductName, Tier: tier}},
		Email:          "operator@example.com",
		SubscriptionID: "sub_123",
		IssuedAt:       now.Add(-time.Minute).Unix(),
		NotBefore:      now.Add(-time.Minute).Unix(),
		ExpiresAt:      now.Add(time.Hour).Unix(),
		Issuer:         "obstalabs-billing",
		KeyID:          "ol-ed25519-primary",
	}
}

func testPrivateKey(seed byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
}

func differentBase64URLChar(current byte) string {
	if current == 'A' {
		return "B"
	}
	return "A"
}

func mustVerifier(t *testing.T, privateKey ed25519.PrivateKey) *Verifier {
	t.Helper()

	publicKey := privateKey.Public().(ed25519.PublicKey)
	verifier, err := NewVerifier(base64.RawURLEncoding.EncodeToString(publicKey))
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return verifier
}

func signLicense(t *testing.T, privateKey ed25519.PrivateKey, payload Payload) string {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	signature := base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(encoded)))

	return LicensePrefix + encoded + "." + signature
}
