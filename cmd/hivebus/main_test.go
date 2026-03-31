package main

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestMainRunsVersionCommand(t *testing.T) {
	t.Helper()

	originalArgs := os.Args
	defer func() {
		os.Args = originalArgs
	}()

	os.Args = []string{"hivebus", "version"}

	main()
}

func TestMainExitsOnInvalidCommand(t *testing.T) {
	t.Helper()

	if os.Getenv("HIVEBUS_MAIN_HELPER") == "1" {
		originalArgs := os.Args
		defer func() {
			os.Args = originalArgs
		}()

		os.Args = []string{"hivebus", "not-a-command"}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestMainExitsOnInvalidCommand")
	cmd.Env = append(os.Environ(), "HIVEBUS_MAIN_HELPER=1")

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected subprocess to exit with an error")
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exit error, got %v", err)
	}

	if exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %d", exitErr.ExitCode())
	}
}
