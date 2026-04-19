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

func TestVerifyHivebusAcceptsUnifiedLicense(t *testing.T) {
	privateKey := testPrivateKey(1)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	verifier, err := NewVerifier(base64.RawURLEncoding.EncodeToString(publicKey))
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	now := time.Unix(1_800_000_000, 0).UTC()
	key := signLicense(t, privateKey, Payload{
		Products: []ProductEntitlement{
			{Product: "verdict", Tier: model.TierPro},
			{Product: "hivebus", Tier: model.TierTeams},
		},
		Email:     "operator@example.com",
		ExpiresAt: now.Add(time.Hour).Unix(),
		SubID:     "sub_123",
	})

	verified, err := verifier.VerifyHivebusAt(key, now)
	if err != nil {
		t.Fatalf("verify hivebus license: %v", err)
	}
	if verified.Entitlement.Product != "hivebus" {
		t.Fatalf("expected hivebus entitlement, got %q", verified.Entitlement.Product)
	}
	if verified.Entitlement.Tier != model.TierTeams {
		t.Fatalf("expected teams tier, got %q", verified.Entitlement.Tier)
	}
	if verified.Payload.Email != "operator@example.com" {
		t.Fatalf("expected payload email to round-trip, got %q", verified.Payload.Email)
	}
}

func TestVerifyHivebusUsesVerifyKeyEnv(t *testing.T) {
	privateKey := testPrivateKey(2)
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

	key := signLicense(t, privateKey, Payload{
		Products:  []ProductEntitlement{{Product: "hivebus", Tier: model.TierPro}},
		ExpiresAt: time.Now().UTC().Add(time.Hour).Unix(),
	})
	if _, err := verifier.VerifyHivebus(key); err != nil {
		t.Fatalf("verify hivebus license: %v", err)
	}
}

func TestVerifyHivebusRejectsLicenseWithoutHivebusEntitlement(t *testing.T) {
	privateKey := testPrivateKey(3)
	verifier := mustVerifier(t, privateKey)

	key := signLicense(t, privateKey, Payload{
		Products:  []ProductEntitlement{{Product: "verdict", Tier: model.TierPro}},
		ExpiresAt: time.Now().UTC().Add(time.Hour).Unix(),
	})

	if _, err := verifier.VerifyHivebus(key); err == nil {
		t.Fatal("expected missing hivebus entitlement to be rejected")
	}
}

func TestVerifyHivebusRejectsOldHivebusPrefix(t *testing.T) {
	privateKey := testPrivateKey(4)
	verifier := mustVerifier(t, privateKey)

	key := signLicense(t, privateKey, Payload{
		Products:  []ProductEntitlement{{Product: "hivebus", Tier: model.TierPro}},
		ExpiresAt: time.Now().UTC().Add(time.Hour).Unix(),
	})
	oldKey := "hb_" + strings.TrimPrefix(key, LicensePrefix)

	if _, err := verifier.VerifyHivebus(oldKey); err == nil {
		t.Fatal("expected old hb_ key to be rejected")
	}
}

func TestVerifyHivebusRejectsTamperedSignature(t *testing.T) {
	privateKey := testPrivateKey(5)
	verifier := mustVerifier(t, privateKey)

	key := signLicense(t, privateKey, Payload{
		Products:  []ProductEntitlement{{Product: "hivebus", Tier: model.TierPro}},
		ExpiresAt: time.Now().UTC().Add(time.Hour).Unix(),
	})
	tampered := key[:len(key)-1] + differentBase64URLChar(key[len(key)-1])

	if _, err := verifier.VerifyHivebus(tampered); err == nil {
		t.Fatal("expected tampered signature to be rejected")
	}
}

func TestVerifyHivebusRejectsExpiredLicense(t *testing.T) {
	privateKey := testPrivateKey(6)
	verifier := mustVerifier(t, privateKey)

	now := time.Unix(1_800_000_000, 0).UTC()
	key := signLicense(t, privateKey, Payload{
		Products:  []ProductEntitlement{{Product: "hivebus", Tier: model.TierPro}},
		ExpiresAt: now.Add(-time.Second).Unix(),
	})

	if _, err := verifier.VerifyHivebusAt(key, now); err == nil {
		t.Fatal("expected expired license to be rejected")
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
