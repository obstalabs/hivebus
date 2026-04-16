package cli

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ppiankov/hivebus/internal/runtime"
)

func TestLoadKeyStoreAllowsExplicitAuthDisabled(t *testing.T) {
	t.Helper()

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

	t.Setenv("HIVEBUS_TOKENS_JSON", "")
	t.Setenv("HIVEBUS_TOKENS_FILE", "")

	if _, err := loadKeyStore("", false); err == nil {
		t.Fatal("loadKeyStore() expected an error")
	}
}

func TestLoadKeyStoreReadsTokenFile(t *testing.T) {
	t.Helper()

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
