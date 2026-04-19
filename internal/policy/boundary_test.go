package policy

import (
	"slices"
	"testing"

	"github.com/ppiankov/hivebus/internal/licensing"
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

	if boundary.DeploymentTierBoundary {
		t.Fatal("expected deployment location to stay outside the pricing boundary")
	}

	if boundary.DeploymentRule == "" {
		t.Fatal("expected deployment rule to be documented")
	}

	if len(boundary.SupportedDeploymentModes) < 3 {
		t.Fatalf("expected multiple supported deployment modes, got %#v", boundary.SupportedDeploymentModes)
	}

	if boundary.TierPositioningByTier[model.TierPro] == "" {
		t.Fatal("expected pro tier positioning")
	}
}

func TestBoundaryDocumentsSharedBillingContract(t *testing.T) {
	t.Helper()

	billing := Boundary().Billing

	if billing.RequiredForCoreRuntime {
		t.Fatal("expected billing to stay optional for core runtime")
	}
	if billing.LicensePrefix != licensing.LicensePrefix {
		t.Fatalf("expected shared license prefix %q, got %q", licensing.LicensePrefix, billing.LicensePrefix)
	}
	if billing.VerifyKeyEnv != licensing.VerifyKeyEnv {
		t.Fatalf("expected verify key env %q, got %q", licensing.VerifyKeyEnv, billing.VerifyKeyEnv)
	}
	if billing.ProductEntitlement != licensing.ProductName {
		t.Fatalf("expected hivebus product entitlement, got %q", billing.ProductEntitlement)
	}
	if billing.EntitlementField != "products[]" {
		t.Fatalf("expected products[] entitlement field, got %q", billing.EntitlementField)
	}
	if billing.CheckoutEndpoint != "/v1/billing/checkout" {
		t.Fatalf("expected checkout endpoint, got %q", billing.CheckoutEndpoint)
	}
	if billing.LicenseEndpoint != "/v1/billing/license" {
		t.Fatalf("expected license retrieval endpoint, got %q", billing.LicenseEndpoint)
	}
	if !slices.Contains(billing.RejectedLegacyPrefixes, "hb_") {
		t.Fatalf("expected old hb_ keys to be rejected, got %#v", billing.RejectedLegacyPrefixes)
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

	if features.DeploymentModel == "" {
		t.Fatal("expected deployment model guidance")
	}
}
