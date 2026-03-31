package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestVersionCommandPrintsBuildInfo(t *testing.T) {
	t.Helper()

	Version = "0.1.0"
	Commit = "abc1234"
	BuildDate = "2026-03-31T00:00:00Z"

	cmd := NewRootCommand()
	buffer := &bytes.Buffer{}
	cmd.SetOut(buffer)
	cmd.SetErr(buffer)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := buffer.String()

	if !strings.Contains(output, "hivebus 0.1.0 (abc1234)") {
		t.Fatalf("unexpected output %q", output)
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
