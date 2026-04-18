package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTaskResultFinalPayloadNormalizesTools(t *testing.T) {
	t.Helper()

	payload := TaskResultFinalPayload{
		ToolsUsed: []string{"go-testing", " go-testing ", "", "docker"},
	}

	tools := payload.NormalizedTools()
	if len(tools) != 2 || tools[0] != "go-testing" || tools[1] != "docker" {
		t.Fatalf("NormalizedTools() = %#v", tools)
	}
}

func TestParseTaskResultFinalPayloadAcceptsStructuredPayload(t *testing.T) {
	t.Helper()

	payload, err := ParseTaskResultFinalPayload(json.RawMessage(`{"status":"done","tools_used":["go-testing","docker"]}`))
	if err != nil {
		t.Fatalf("ParseTaskResultFinalPayload() error = %v", err)
	}
	if !payload.Successful() {
		t.Fatal("expected successful payload")
	}
	if len(payload.NormalizedTools()) != 2 {
		t.Fatalf("expected 2 normalized tools, got %#v", payload.NormalizedTools())
	}
}

func TestDiscoveredCapabilityValidateRequiresApprovalForTrusted(t *testing.T) {
	t.Helper()

	record := DiscoveredCapability{
		WorkerID:         "worker.smokevm",
		CapabilityID:     "go-testing",
		Requirements:     []string{"go", "fs"},
		RiskLevel:        CapabilityRiskLow,
		TrustLevel:       CapabilityTrustTrusted,
		ObservationCount: 3,
		SuccessCount:     3,
		FirstObservedAt:  time.Date(2026, 4, 18, 0, 0, 0, 0, time.UTC),
		LastObservedAt:   time.Date(2026, 4, 18, 1, 0, 0, 0, time.UTC),
	}

	if err := record.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want approval validation")
	}
}
