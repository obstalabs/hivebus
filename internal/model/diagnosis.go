package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Confidence describes how safe it is to turn a diagnosis into a WO.
type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

var validConfidenceLevels = []Confidence{
	ConfidenceLow,
	ConfidenceMedium,
	ConfidenceHigh,
}

// Diagnosis is the structured output of an investigation thread.
type Diagnosis struct {
	Problem             string     `json:"problem"`
	LikelyCause         string     `json:"likely_cause"`
	ProposedRemediation []string   `json:"proposed_remediation"`
	MissingInfo         []string   `json:"missing_info,omitempty"`
	EvidenceIDs         []string   `json:"evidence_ids"`
	Confidence          Confidence `json:"confidence"`
	Verified            bool       `json:"verified"`
}

// Validate keeps speculation out of WO creation by forcing structured evidence.
func (d Diagnosis) Validate() error {
	switch {
	case strings.TrimSpace(d.Problem) == "":
		return errors.New("problem is required")
	case strings.TrimSpace(d.LikelyCause) == "":
		return errors.New("likely_cause is required")
	case len(d.ProposedRemediation) == 0:
		return errors.New("at least one remediation step is required")
	case len(d.EvidenceIDs) == 0:
		return errors.New("at least one evidence id is required")
	case !slices.Contains(validConfidenceLevels, d.Confidence):
		return fmt.Errorf("unsupported confidence %q", d.Confidence)
	}

	for _, step := range d.ProposedRemediation {
		if strings.TrimSpace(step) == "" {
			return errors.New("remediation steps must not be empty")
		}
	}

	// WO-68: evidence-backed diagnoses must name concrete evidence handles, not
	// placeholder entries that only satisfy array length.
	for _, evidenceID := range d.EvidenceIDs {
		if strings.TrimSpace(evidenceID) == "" {
			return errors.New("evidence ids must not be empty")
		}
	}

	for _, info := range d.MissingInfo {
		if strings.TrimSpace(info) == "" {
			return errors.New("missing_info entries must not be empty")
		}
	}

	return nil
}
