package cli

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ppiankov/hivebus/internal/runtime"
)

func TestLoadKeyStoreAllowsExplicitAuthDisabled(t *testing.T) {
	t.Helper()

	t.Setenv("HIVEBUS_API_VERIFY_KEY", "")
	t.Setenv("HIVEBUS_TOKENS_JSON", "")
	t.Setenv("HIVEBUS_TOKENS_FILE", "")

	keys, err := loadKeyStore("", true)
	if err != nil {
		t.Fatalf("loadKeyStore() error = %v", err)
	}
	if keys != nil {
		t.Fatalf("expected nil keystore when auth disabled, got %#v", keys)
	}
}

func TestLoadKeyStoreParsesEnvJSON(t *testing.T) {
	t.Helper()

	t.Setenv("HIVEBUS_API_VERIFY_KEY", "")
	entries := []runtime.TokenEntry{
		{ID: "worker", KeyHash: runtime.HashToken("worker-secret"), Role: runtime.RoleWorker},
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("Marshal(entries) error = %v", err)
	}

	t.Setenv("HIVEBUS_TOKENS_JSON", string(data))
	t.Setenv("HIVEBUS_TOKENS_FILE", "")

	keys, err := loadKeyStore("", false)
	if err != nil {
		t.Fatalf("loadKeyStore() error = %v", err)
	}
	if keys == nil || keys.Lookup("worker-secret") == nil {
		t.Fatalf("expected worker token to be loaded")
	}
}

func TestLoadKeyStoreRequiresSourceUnlessDisabled(t *testing.T) {
	t.Helper()

	t.Setenv("HIVEBUS_API_VERIFY_KEY", "")
	t.Setenv("HIVEBUS_TOKENS_JSON", "")
	t.Setenv("HIVEBUS_TOKENS_FILE", "")
	restoreBuiltIn := runtime.BuiltInAPIVerifyKey
	runtime.BuiltInAPIVerifyKey = ""
	defer func() {
		runtime.BuiltInAPIVerifyKey = restoreBuiltIn
	}()

	if _, err := loadKeyStore("", false); err == nil {
		t.Fatal("loadKeyStore() expected an error")
	}
}

func TestLoadKeyStoreReadsTokenFile(t *testing.T) {
	t.Helper()

	t.Setenv("HIVEBUS_API_VERIFY_KEY", "")
	t.Setenv("HIVEBUS_TOKENS_JSON", "")
	t.Setenv("HIVEBUS_TOKENS_FILE", "")

	entries := []runtime.TokenEntry{
		{ID: "operator", KeyHash: runtime.HashToken("operator-secret"), Role: runtime.RoleOperator},
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("Marshal(entries) error = %v", err)
	}

	path := t.TempDir() + "/tokens.json"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	keys, err := loadKeyStore(path, false)
	if err != nil {
		t.Fatalf("loadKeyStore() error = %v", err)
	}
	if keys == nil || keys.Lookup("operator-secret") == nil {
		t.Fatalf("expected operator token to be loaded")
	}
}

func TestLoadKeyStoreParsesSignedVerifyKeyFromEnv(t *testing.T) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	t.Setenv("HIVEBUS_API_VERIFY_KEY", base64.StdEncoding.EncodeToString(publicKey))
	t.Setenv("HIVEBUS_TOKENS_JSON", "")
	t.Setenv("HIVEBUS_TOKENS_FILE", "")

	keys, err := loadKeyStore("", false)
	if err != nil {
		t.Fatalf("loadKeyStore() error = %v", err)
	}

	token := mustSignedToken(t, privateKey, runtime.SignedTokenClaims{
		Subject:   "operator.sig",
		Role:      runtime.RoleOperator,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if entry := keys.Lookup(token); entry == nil || entry.ID != "operator.sig" {
		t.Fatalf("expected signed operator token to validate, got %#v", entry)
	}
}

func TestLoadKeyStoreUsesBuiltInVerifyKeyWhenEnvIsUnset(t *testing.T) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	t.Setenv("HIVEBUS_API_VERIFY_KEY", "")
	t.Setenv("HIVEBUS_TOKENS_JSON", "")
	t.Setenv("HIVEBUS_TOKENS_FILE", "")

	restoreBuiltIn := runtime.BuiltInAPIVerifyKey
	runtime.BuiltInAPIVerifyKey = base64.StdEncoding.EncodeToString(publicKey)
	defer func() {
		runtime.BuiltInAPIVerifyKey = restoreBuiltIn
	}()

	keys, err := loadKeyStore("", false)
	if err != nil {
		t.Fatalf("loadKeyStore() error = %v", err)
	}

	token := mustSignedToken(t, privateKey, runtime.SignedTokenClaims{
		Subject:   "worker.sig",
		Role:      runtime.RoleWorker,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if entry := keys.Lookup(token); entry == nil || entry.ID != "worker.sig" {
		t.Fatalf("expected signed worker token to validate, got %#v", entry)
	}
}

func TestLoadKeyStoreRejectsMixedSignedAndLegacySources(t *testing.T) {
	t.Helper()

	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	t.Setenv("HIVEBUS_API_VERIFY_KEY", base64.StdEncoding.EncodeToString(publicKey))
	t.Setenv("HIVEBUS_TOKENS_JSON", "[]")
	t.Setenv("HIVEBUS_TOKENS_FILE", "")

	if _, err := loadKeyStore("", false); err == nil {
		t.Fatal("loadKeyStore() expected an error for mixed auth sources")
	}
}

func mustSignedToken(
	t *testing.T,
	privateKey ed25519.PrivateKey,
	claims runtime.SignedTokenClaims,
) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal(claims) error = %v", err)
	}

	signature := ed25519.Sign(privateKey, payload)
	return "hbk1." +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(signature)
}
