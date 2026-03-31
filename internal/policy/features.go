package policy

import "github.com/ppiankov/hivebus/internal/model"

// FeatureDescriptor defines which repo owns a capability and which tiers receive it.
type FeatureDescriptor struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Description   string       `json:"description"`
	Repo          string       `json:"repo"`
	IncludedTiers []model.Tier `json:"included_tiers"`
}

// WorkledgerOperation describes one required workledger interaction.
type WorkledgerOperation struct {
	ID            string       `json:"id"`
	Method        string       `json:"method"`
	Path          string       `json:"path"`
	Purpose       string       `json:"purpose"`
	Required      bool         `json:"required"`
	IncludedTiers []model.Tier `json:"included_tiers"`
}

// WorkledgerContract keeps workledger canonical and Hiveram optional.
type WorkledgerContract struct {
	System             string                `json:"system"`
	Canonical          bool                  `json:"canonical"`
	OptionalProjection string                `json:"optional_projection,omitempty"`
	ProjectSelection   string                `json:"project_selection"`
	Operations         []WorkledgerOperation `json:"operations"`
}

// FeatureBoundary declares which capabilities live in the community repo vs hivebus-pro.
type FeatureBoundary struct {
	Model             string              `json:"model"`
	CommunityFeatures []FeatureDescriptor `json:"community_features"`
	PaidOnlyFeatures  []FeatureDescriptor `json:"paid_only_features"`
	Workledger        WorkledgerContract  `json:"workledger"`
}

// Features returns the explicit community vs paid boundary for Hivebus.
func Features() FeatureBoundary {
	allTiers := []model.Tier{
		model.TierFree,
		model.TierPro,
		model.TierTeams,
		model.TierEnterprise,
	}

	paidTiers := []model.Tier{
		model.TierPro,
		model.TierTeams,
		model.TierEnterprise,
	}

	teamAndEnterpriseTiers := []model.Tier{
		model.TierTeams,
		model.TierEnterprise,
	}

	return FeatureBoundary{
		Model: "maintenance-focused community core in hivebus, paid-only capability in hivebus-pro",
		CommunityFeatures: []FeatureDescriptor{
			{
				ID:            "typed-thread-protocol",
				Name:          "Typed thread protocol",
				Description:   "Structured JSON envelopes, artifacts, receipts, and lifecycle state that every agent speaks the same way.",
				Repo:          "hivebus",
				IncludedTiers: allTiers,
			},
			{
				ID:            "self-hosted-bus-core",
				Name:          "Self-hosted bus core",
				Description:   "The deterministic, self-hosted coordination core that agents can run without a hosted dependency.",
				Repo:          "hivebus",
				IncludedTiers: allTiers,
			},
			{
				ID:            "capability-routing-core",
				Name:          "Capability routing core",
				Description:   "Explicit recipient routing and capability-aware dispatch primitives that keep cross-agent traffic structured.",
				Repo:          "hivebus",
				IncludedTiers: allTiers,
			},
			{
				ID:            "nullbot-intake-core",
				Name:          "Nullbot intake core",
				Description:   "Basic issue intake, evidence packaging, and clarification loops that feed the bus from the edge.",
				Repo:          "hivebus",
				IncludedTiers: allTiers,
			},
			{
				ID:            "canonical-workledger-bridge",
				Name:          "Canonical workledger bridge",
				Description:   "Search, create, update, note, claim, release, and context-sync operations against workledger as the execution source of truth.",
				Repo:          "hivebus",
				IncludedTiers: allTiers,
			},
		},
		PaidOnlyFeatures: []FeatureDescriptor{
			{
				ID:            "hiveram-commercial-sync",
				Name:          "Hiveram commercial sync",
				Description:   "Optional projection of canonical workledger work orders into hiveram.com commercial workflows.",
				Repo:          "hivebus-pro",
				IncludedTiers: paidTiers,
			},
			{
				ID:            "managed-control-plane",
				Name:          "Managed control plane",
				Description:   "Hosted relay, managed storage, and operations surfaces that remove self-hosting overhead.",
				Repo:          "hivebus-pro",
				IncludedTiers: paidTiers,
			},
			{
				ID:            "adaptive-optimization",
				Name:          "Adaptive Optimization",
				Description:   "Privacy-safe background analysis, selective hints, and escalation from structured agent-usage patterns.",
				Repo:          "hivebus-pro",
				IncludedTiers: paidTiers,
			},
			{
				ID:            "team-org-policy",
				Name:          "Team and org policy",
				Description:   "Shared queues, RBAC, policy packs, and multi-operator coordination surfaces for teams.",
				Repo:          "hivebus-pro",
				IncludedTiers: teamAndEnterpriseTiers,
			},
			{
				ID:            "enterprise-compliance-pack",
				Name:          "Enterprise compliance pack",
				Description:   "BYOK, long retention, regional controls, and audit export surfaces for enterprise deployments.",
				Repo:          "hivebus-pro",
				IncludedTiers: []model.Tier{model.TierEnterprise},
			},
		},
		Workledger: WorkledgerContract{
			System:             "workledger",
			Canonical:          true,
			OptionalProjection: "hiveram.com",
			ProjectSelection:   "explicit workledger project required when promoting a verified diagnosis to execution",
			Operations: []WorkledgerOperation{
				{
					ID:            "search-work-orders",
					Method:        "GET",
					Path:          "/api/v1/search?q=<query>&project=<project>",
					Purpose:       "Check for an existing WO before creating a duplicate.",
					Required:      true,
					IncludedTiers: allTiers,
				},
				{
					ID:            "create-work-order",
					Method:        "POST",
					Path:          "/api/v1/wo",
					Purpose:       "Create the canonical work order from a verified diagnosis.",
					Required:      true,
					IncludedTiers: allTiers,
				},
				{
					ID:            "update-work-order",
					Method:        "PATCH",
					Path:          "/api/v1/wo/{project}/{id}",
					Purpose:       "Update WO status, priority, or sections as investigation evolves.",
					Required:      true,
					IncludedTiers: allTiers,
				},
				{
					ID:            "add-work-order-note",
					Method:        "POST",
					Path:          "/api/v1/wo/{project}/{id}/note",
					Purpose:       "Attach agent findings, commit SHAs, and resolution notes to the canonical record.",
					Required:      true,
					IncludedTiers: allTiers,
				},
				{
					ID:            "claim-work-order",
					Method:        "POST",
					Path:          "/api/v1/wo/{project}/{id}/claim",
					Purpose:       "Avoid duplicate execution when multiple agents coordinate on the same issue.",
					Required:      true,
					IncludedTiers: allTiers,
				},
				{
					ID:            "release-work-order",
					Method:        "POST",
					Path:          "/api/v1/wo/{project}/{id}/release",
					Purpose:       "Release exclusive claims when execution is complete or handed off.",
					Required:      true,
					IncludedTiers: allTiers,
				},
				{
					ID:            "context-sync",
					Method:        "PUT/GET",
					Path:          "/api/v1/blob/{project}/{key}",
					Purpose:       "Share thread context and machine memory across agents or operator machines.",
					Required:      true,
					IncludedTiers: allTiers,
				},
				{
					ID:            "project-meta-link",
					Method:        "PATCH",
					Path:          "/api/v1/projects/{project}/meta",
					Purpose:       "Link project metadata such as repo path or commercial projection state.",
					Required:      false,
					IncludedTiers: allTiers,
				},
			},
		},
	}
}
