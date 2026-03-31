package model

import "testing"

func TestDiagnosisValidateAcceptsVerifiedDiagnosis(t *testing.T) {
	t.Helper()

	diagnosis := Diagnosis{
		Problem:             "The API returns 502 responses.",
		LikelyCause:         "The database connection pool is exhausted.",
		ProposedRemediation: []string{"Reduce worker fan-out.", "Raise the connection pool ceiling."},
		EvidenceIDs:         []string{"art_logs"},
		Confidence:          ConfidenceHigh,
		Verified:            true,
	}

	if err := diagnosis.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDiagnosisValidateRejectsUnknownConfidence(t *testing.T) {
	t.Helper()

	diagnosis := Diagnosis{
		Problem:             "The API returns 502 responses.",
		LikelyCause:         "The database connection pool is exhausted.",
		ProposedRemediation: []string{"Reduce worker fan-out."},
		EvidenceIDs:         []string{"art_logs"},
		Confidence:          Confidence("wild"),
	}

	if err := diagnosis.Validate(); err == nil {
		t.Fatal("Validate() expected an error")
	}
}

func TestDiagnosisValidateRejectsMissingRequiredFields(t *testing.T) {
	t.Helper()

	testCases := []Diagnosis{
		{
			LikelyCause:         "The database connection pool is exhausted.",
			ProposedRemediation: []string{"Reduce worker fan-out."},
			EvidenceIDs:         []string{"art_logs"},
			Confidence:          ConfidenceHigh,
		},
		{
			Problem:             "The API returns 502 responses.",
			ProposedRemediation: []string{"Reduce worker fan-out."},
			EvidenceIDs:         []string{"art_logs"},
			Confidence:          ConfidenceHigh,
		},
		{
			Problem:     "The API returns 502 responses.",
			LikelyCause: "The database connection pool is exhausted.",
			EvidenceIDs: []string{"art_logs"},
			Confidence:  ConfidenceHigh,
		},
		{
			Problem:             "The API returns 502 responses.",
			LikelyCause:         "The database connection pool is exhausted.",
			ProposedRemediation: []string{"Reduce worker fan-out."},
			Confidence:          ConfidenceHigh,
		},
		{
			Problem:             "The API returns 502 responses.",
			LikelyCause:         "The database connection pool is exhausted.",
			ProposedRemediation: []string{""},
			EvidenceIDs:         []string{"art_logs"},
			Confidence:          ConfidenceHigh,
		},
		{
			Problem:             "The API returns 502 responses.",
			LikelyCause:         "The database connection pool is exhausted.",
			ProposedRemediation: []string{"Reduce worker fan-out."},
			EvidenceIDs:         []string{"art_logs"},
			MissingInfo:         []string{""},
			Confidence:          ConfidenceHigh,
		},
	}

	for _, diagnosis := range testCases {
		if err := diagnosis.Validate(); err == nil {
			t.Fatalf("Validate() expected an error for %#v", diagnosis)
		}
	}
}
