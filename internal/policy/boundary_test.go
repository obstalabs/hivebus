package policy

import (
	"testing"

	"github.com/ppiankov/hivebus/internal/model"
)

func TestBoundarySplitsFreeAndCommercialRepos(t *testing.T) {
	t.Helper()

	boundary := Boundary()

	if boundary.OpenSourceRepo != "hivebus" {
		t.Fatalf("expected open source repo hivebus, got %q", boundary.OpenSourceRepo)
	}

	if boundary.CommercialRepo != "hivebus-pro" {
		t.Fatalf("expected commercial repo hivebus-pro, got %q", boundary.CommercialRepo)
	}

	if boundary.RepoByTier[model.TierFree] != "hivebus" {
		t.Fatalf("expected free tier in hivebus, got %q", boundary.RepoByTier[model.TierFree])
	}

	if boundary.RepoByTier[model.TierEnterprise] != "hivebus-pro" {
		t.Fatalf("expected enterprise tier in hivebus-pro, got %q", boundary.RepoByTier[model.TierEnterprise])
	}
}

func TestRepoForTierReturnsConfiguredRepo(t *testing.T) {
	t.Helper()

	if repo := RepoForTier(model.TierPro); repo != "hivebus-pro" {
		t.Fatalf("expected pro repo hivebus-pro, got %q", repo)
	}
}

func TestFeaturesExposeCommunityAndPaidSplit(t *testing.T) {
	t.Helper()

	features := Features()

	if len(features.CommunityFeatures) == 0 {
		t.Fatal("expected community features")
	}

	if len(features.PaidOnlyFeatures) == 0 {
		t.Fatal("expected paid-only features")
	}

	if features.Workledger.System != "workledger" {
		t.Fatalf("expected workledger system, got %q", features.Workledger.System)
	}

	if !features.Workledger.Canonical {
		t.Fatal("expected workledger to be canonical")
	}
}
