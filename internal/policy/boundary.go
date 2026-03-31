package policy

import "github.com/ppiankov/hivebus/internal/model"

// EditionBoundary keeps pricing boundaries structural instead of aspirational.
type EditionBoundary struct {
	OpenSourceRepo string                `json:"open_source_repo"`
	CommercialRepo string                `json:"commercial_repo"`
	RepoByTier     map[model.Tier]string `json:"repo_by_tier"`
}

var repoByTier = map[model.Tier]string{
	model.TierFree:       "hivebus",
	model.TierPro:        "hivebus-pro",
	model.TierTeams:      "hivebus-pro",
	model.TierEnterprise: "hivebus-pro",
}

// Boundary returns the repository split for free vs commercial editions.
func Boundary() EditionBoundary {
	return EditionBoundary{
		OpenSourceRepo: "hivebus",
		CommercialRepo: "hivebus-pro",
		RepoByTier: map[model.Tier]string{
			model.TierFree:       repoByTier[model.TierFree],
			model.TierPro:        repoByTier[model.TierPro],
			model.TierTeams:      repoByTier[model.TierTeams],
			model.TierEnterprise: repoByTier[model.TierEnterprise],
		},
	}
}

// RepoForTier returns the repository that owns the given tier.
func RepoForTier(tier model.Tier) string {
	return repoByTier[tier]
}
