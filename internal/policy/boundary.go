package policy

import "github.com/ppiankov/hivebus/internal/model"

// EditionBoundary keeps pricing boundaries structural instead of aspirational.
type EditionBoundary struct {
	OpenSourceRepo           string                `json:"open_source_repo"`
	CommercialRepo           string                `json:"commercial_repo"`
	RepoByTier               map[model.Tier]string `json:"repo_by_tier"`
	DeploymentTierBoundary   bool                  `json:"deployment_tier_boundary"`
	DeploymentRule           string                `json:"deployment_rule"`
	TierPositioningByTier    map[model.Tier]string `json:"tier_positioning_by_tier"`
	SupportedDeploymentModes []string              `json:"supported_deployment_modes"`
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
		OpenSourceRepo:         "hivebus",
		CommercialRepo:         "hivebus-pro",
		DeploymentTierBoundary: false,
		DeploymentRule:         "deployment location is flexible; edition boundaries follow coordination and policy complexity instead of local, Fly, VPS, or private infrastructure choices",
		RepoByTier: map[model.Tier]string{
			model.TierFree:       repoByTier[model.TierFree],
			model.TierPro:        repoByTier[model.TierPro],
			model.TierTeams:      repoByTier[model.TierTeams],
			model.TierEnterprise: repoByTier[model.TierEnterprise],
		},
		TierPositioningByTier: map[model.Tier]string{
			model.TierFree:       "self-hostable protocol core and canonical workledger bridge",
			model.TierPro:        "single-tenant coordination and commercial runtime features on any deployment target",
			model.TierTeams:      "shared coordination surfaces, multi-operator policy, and team-scale routing",
			model.TierEnterprise: "corporate controls, compliance boundaries, and enterprise governance",
		},
		SupportedDeploymentModes: []string{
			"local",
			"fly",
			"vps",
			"private-infrastructure",
		},
	}
}

// RepoForTier returns the repository that owns the given tier.
func RepoForTier(tier model.Tier) string {
	return repoByTier[tier]
}
