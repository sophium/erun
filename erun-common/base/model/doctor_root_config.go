package model

// RootConfigStatus gates doctor: non-OK means restore before alias repair.
type RootConfigStatus string

const (
	RootConfigStatusOK        RootConfigStatus = "ok"
	RootConfigStatusMissing   RootConfigStatus = "missing"
	RootConfigStatusCorrupted RootConfigStatus = "corrupted"
)

// OrphanedAliasContextRef is a cloud context referencing a missing provider alias;
// Region seeds provider re-init.
type OrphanedAliasContextRef struct {
	Name   string `json:"name"`
	Region string `json:"region"`
}

// OrphanedAlias collects every reference to a missing alias.
type OrphanedAlias struct {
	Alias                     string                    `json:"alias"`
	Username                  string                    `json:"username,omitempty"`
	AccountID                 string                    `json:"accountId,omitempty"`
	Provider                  string                    `json:"provider,omitempty"`
	Parsed                    bool                      `json:"parsed"`
	ReferencedByTenants       []string                  `json:"referencedByTenants,omitempty"`
	ReferencedByCloudContexts []OrphanedAliasContextRef `json:"referencedByCloudContexts,omitempty"`
}

// RootConfigInspection is the read-only root-config view shared by CLI and MCP doctors.
type RootConfigInspection struct {
	ConfigPath       string                 `json:"configPath"`
	ConfigStatus     RootConfigStatus       `json:"configStatus"`
	ConfigError      string                 `json:"configError,omitempty"`
	OrphanedAliases  []OrphanedAlias        `json:"orphanedAliases,omitempty"`
	OrphanedContexts []OrphanedCloudContext `json:"orphanedCloudContexts,omitempty"`
	Backups          []ConfigBackup         `json:"backups,omitempty"`
	ConfiguredCount  int                    `json:"configuredProviderCount"`
	CloudContextHits int                    `json:"cloudContextCount"`
	TenantHits       int                    `json:"tenantCount"`
}

// OrphanedCloudContextEnvRef is an env referencing a missing cloud context.
type OrphanedCloudContextEnvRef struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
}

// OrphanedCloudContext aggregates env references to a missing cloud context.
type OrphanedCloudContext struct {
	KubernetesContext  string                       `json:"kubernetesContext"`
	AccountID          string                       `json:"accountId,omitempty"`
	Region             string                       `json:"region,omitempty"`
	CloudProviderAlias string                       `json:"cloudProviderAlias,omitempty"`
	ReferencedByEnvs   []OrphanedCloudContextEnvRef `json:"referencedByEnvs,omitempty"`
}

func (r RootConfigInspection) Complete() bool {
	if r.ConfigStatus != RootConfigStatusOK {
		return false
	}
	if len(r.OrphanedAliases) != 0 {
		return false
	}
	return len(r.OrphanedContexts) == 0
}

// RepairOrphanedAliasParams carries the fields needed to re-init one orphan's provider.
type RepairOrphanedAliasParams struct {
	Orphan        OrphanedAlias
	SSORegion     string
	SSOStartURL   string
	RoleName      string
	Region        string
	OIDCIssuerURL string
	SkipLogin     bool
}
