package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestVersionCommandPrintsBuildInfo(t *testing.T) {
	t.Helper()

	oldVersion := Version
	oldBinarySHA := BinarySHA
	oldBinaryBuiltAt := BinaryBuiltAt
	t.Cleanup(func() {
		Version = oldVersion
		BinarySHA = oldBinarySHA
		BinaryBuiltAt = oldBinaryBuiltAt
	})
	Version = "0.1.0"
	BinarySHA = "abc1234"
	BinaryBuiltAt = "2026-03-31T00:00:00Z"

	cmd := NewRootCommand()
	buffer := &bytes.Buffer{}
	cmd.SetOut(buffer)
	cmd.SetErr(buffer)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buffer.String()

	for _, want := range []string{
		"hivebus 0.1.0",
		"binary_sha abc1234",
		"binary_built_at 2026-03-31T00:00:00Z",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("version output missing %q:\n%s", want, output)
		}
	}
}

func TestVersionCommandJSONPrintsBuildInfo(t *testing.T) {
	t.Helper()

	oldVersion := Version
	oldBinarySHA := BinarySHA
	oldBinaryBuiltAt := BinaryBuiltAt
	t.Cleanup(func() {
		Version = oldVersion
		BinarySHA = oldBinarySHA
		BinaryBuiltAt = oldBinaryBuiltAt
	})
	Version = "0.1.0"
	BinarySHA = "abc1234"
	BinaryBuiltAt = "2026-03-31T00:00:00Z"

	cmd := NewRootCommand()
	buffer := &bytes.Buffer{}
	cmd.SetOut(buffer)
	cmd.SetErr(buffer)
	cmd.SetArgs([]string{"version", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var got versionOutput
	if err := json.Unmarshal(buffer.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\n%s", err, buffer.String())
	}
	if got.Version != "0.1.0" {
		t.Fatalf("version: got %q", got.Version)
	}
	if got.BinarySHA != "abc1234" {
		t.Fatalf("binary_sha: got %q", got.BinarySHA)
	}
	if got.BinaryBuiltAt != "2026-03-31T00:00:00Z" {
		t.Fatalf("binary_built_at: got %q", got.BinaryBuiltAt)
	}
}

func TestSpecCommandPrintsWorkledgerTracking(t *testing.T) {
	t.Helper()

	cmd := NewRootCommand()
	buffer := &bytes.Buffer{}
	cmd.SetOut(buffer)
	cmd.SetErr(buffer)
	cmd.SetArgs([]string{"spec"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buffer.String()

	if !strings.Contains(output, "\"tracking_system\": \"workledger\"") {
		t.Fatalf("unexpected output %q", output)
	}
}

func TestSampleCasePrintsOptionalCommercialSync(t *testing.T) {
	t.Helper()

	cmd := NewRootCommand()
	buffer := &bytes.Buffer{}
	cmd.SetOut(buffer)
	cmd.SetErr(buffer)
	cmd.SetArgs([]string{"sample-case"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buffer.String()

	if !strings.Contains(output, "\"optional_sync_targets\": [") {
		t.Fatalf("unexpected output %q", output)
	}
}

func TestExecuteRunsVersionCommand(t *testing.T) {
	t.Helper()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	os.Args = []string{"hivebus", "version"}

	if err := Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}
