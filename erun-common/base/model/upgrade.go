package model

// UpgradeTarget scopes an upgrade run; VersionOverride targets every env without a registry lookup.
type UpgradeTarget struct {
	Tenant          string
	Environment     string
	VersionOverride string
	Force           bool
	// GateEnvironment names the tenant's merge-queue gate, which erun does not store. It is always
	// included and rolled first. Requires Tenant and must exist in it.
	GateEnvironment string
	// Fleet includes every non-host environment in Tenant regardless of opt-in. Requires Tenant.
	Fleet bool
}

// UpgradeVersionCandidate is one newer version for an env and the registry that offered it.
type UpgradeVersionCandidate struct {
	Version  string `json:"version"`
	Registry string `json:"registry,omitempty"`
}

// UpgradePlanItem is one environment's upgrade decision; an empty Target is up to date,
// unresolved, or awaiting a pick, per UnresolvedReason.
type UpgradePlanItem struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Channel     string `json:"channel"`
	Current     string `json:"current"`
	Target      string `json:"target"`
	Lagging     bool   `json:"lagging"`
	// Candidates holds the distinct newer versions; more than one needs a pick.
	Candidates       []UpgradeVersionCandidate `json:"candidates,omitempty"`
	UnresolvedReason string                    `json:"unresolvedReason,omitempty"`
	// IsGate marks the environment named by UpgradeTarget.GateEnvironment.
	IsGate bool `json:"isGate,omitempty"`
}
