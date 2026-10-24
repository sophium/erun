package model

// EnvironmentVersionStatus is one environment's erun version against the tenant's newest.
type EnvironmentVersionStatus struct {
	Environment string `json:"environment"`
	// Version comes from cached config, else a live helm read. Empty is a confirmed absence unless
	// VersionUnresolved is set.
	Version string `json:"version,omitempty"`
	// BehindMax is set only when both versions parse as plain semver.
	BehindMax bool `json:"behindMax,omitempty"`
	// VersionUnresolved means neither config nor the cluster could answer; excluded from MaxVersion.
	VersionUnresolved       bool   `json:"versionUnresolved,omitempty"`
	VersionUnresolvedReason string `json:"versionUnresolvedReason,omitempty"`
}

// TenantVersionDrift compares environments within one tenant, not against the registry.
// GateEnvironment additionally flags a merge-queue gate older than an environment it gates.
type TenantVersionDrift struct {
	Tenant       string                     `json:"tenant"`
	Environments []EnvironmentVersionStatus `json:"environments,omitempty"`
	MaxVersion   string                     `json:"maxVersion,omitempty"`

	GateEnvironment string `json:"gateEnvironment,omitempty"`
	GateVersion     string `json:"gateVersion,omitempty"`
	// GateVersionUnresolved means GateBehind is not a real verdict.
	GateVersionUnresolved bool `json:"gateVersionUnresolved,omitempty"`
	// GateVersionUnresolvedReason is also empty for a parsed non-semver (snapshot) version.
	GateVersionUnresolvedReason string `json:"gateVersionUnresolvedReason,omitempty"`
	GateBehind                  bool   `json:"gateBehind,omitempty"`
	// GateOutdatedBy names environments running a newer erun version than the gate.
	GateOutdatedBy []string `json:"gateOutdatedBy,omitempty"`
}
