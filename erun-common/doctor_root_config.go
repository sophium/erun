package eruncommon

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// RootConfigStatus categorizes the on-disk root config's load state.
// It is the doctor's first signal: "ok" means we can move on to
// looking for orphaned references, anything else means the file
// itself needs a restore before alias-level repair makes sense.
type RootConfigStatus string

const (
	RootConfigStatusOK        RootConfigStatus = "ok"
	RootConfigStatusMissing   RootConfigStatus = "missing"
	RootConfigStatusCorrupted RootConfigStatus = "corrupted"
)

// OrphanedAliasContextRef names one cloud-context entry that points
// at a missing root cloud-provider alias. The Region is captured so a
// repair flow can pre-fill InitAWSCloudProviderParams.Region (it is
// the only piece of context-side state that overlaps with provider
// init params).
type OrphanedAliasContextRef struct {
	Name   string `json:"name"`
	Region string `json:"region"`
}

// OrphanedAlias collects every back-reference for a single missing
// alias plus the decoded username/account/provider triple. The
// decoded fields are populated when ParseCloudProviderAlias succeeds;
// otherwise they remain empty and Parsed is false, which tells the
// repair flow it cannot auto-seed an init call.
type OrphanedAlias struct {
	Alias                     string                    `json:"alias"`
	Username                  string                    `json:"username,omitempty"`
	AccountID                 string                    `json:"accountId,omitempty"`
	Provider                  string                    `json:"provider,omitempty"`
	Parsed                    bool                      `json:"parsed"`
	ReferencedByTenants       []string                  `json:"referencedByTenants,omitempty"`
	ReferencedByCloudContexts []OrphanedAliasContextRef `json:"referencedByCloudContexts,omitempty"`
}

// RootConfigInspection is the structured view of the root config
// state used by both the CLI doctor and the MCP doctor tool. It is
// purely descriptive: nothing in this file performs side effects.
type RootConfigInspection struct {
	ConfigPath       string             `json:"configPath"`
	ConfigStatus     RootConfigStatus   `json:"configStatus"`
	ConfigError      string             `json:"configError,omitempty"`
	OrphanedAliases  []OrphanedAlias    `json:"orphanedAliases,omitempty"`
	Backups          []RootConfigBackup `json:"backups,omitempty"`
	ConfiguredCount  int                `json:"configuredProviderCount"`
	CloudContextHits int                `json:"cloudContextCount"`
	TenantHits       int                `json:"tenantCount"`
}

// Complete reports whether the root config is in a state the rest of
// erun can rely on. False when the file failed to load OR when any
// orphan reference was found.
func (r RootConfigInspection) Complete() bool {
	if r.ConfigStatus != RootConfigStatusOK {
		return false
	}
	return len(r.OrphanedAliases) == 0
}

// RootConfigInspectionStore is the read-only surface InspectRootConfig
// needs. Kept narrow on purpose: the inspection must work even when
// the root config is unloadable, so it leans on ListTenantConfigs
// (which reads tenant-level files independently) and a direct
// LoadERunConfig probe for the central file.
type RootConfigInspectionStore interface {
	LoadERunConfig() (ERunConfig, string, error)
	ListTenantConfigs() ([]TenantConfig, error)
}

// InspectRootConfig walks the root config and every tenant config to
// surface dangling cloud-provider-alias references. The walk does not
// mutate anything; on a healthy install it produces an inspection
// with status=ok, zero orphans, and (when present) the available
// daily backup list so doctor can offer rollback as an option even on
// a healthy system.
func InspectRootConfig(store RootConfigInspectionStore) (RootConfigInspection, error) {
	if store == nil {
		return RootConfigInspection{}, errors.New("store is required")
	}
	config, configPath, err := store.LoadERunConfig()
	inspection := RootConfigInspection{
		ConfigPath: configPath,
	}
	switch {
	case err == nil:
		inspection.ConfigStatus = RootConfigStatusOK
	case errors.Is(err, ErrNotInitialized):
		inspection.ConfigStatus = RootConfigStatusMissing
		inspection.ConfigError = err.Error()
	case errors.Is(err, ErrConfigCorrupted):
		inspection.ConfigStatus = RootConfigStatusCorrupted
		inspection.ConfigError = err.Error()
	default:
		return RootConfigInspection{}, err
	}

	if backups, listErr := ListRootConfigBackups(configPath); listErr == nil {
		inspection.Backups = backups
	}

	// Only walk references when the root config loaded cleanly. With
	// a corrupted or missing root we have no source of truth for the
	// CloudProviders list to compare against — every alias reference
	// would look orphaned, which is noise. The recommended action in
	// that state is "restore from backup," surfaced separately via
	// Backups.
	if inspection.ConfigStatus != RootConfigStatusOK {
		return inspection, nil
	}

	configured := make(map[string]struct{}, len(config.CloudProviders))
	for _, provider := range config.CloudProviders {
		alias := strings.TrimSpace(provider.Alias)
		if alias == "" {
			continue
		}
		configured[alias] = struct{}{}
	}
	inspection.ConfiguredCount = len(configured)

	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return RootConfigInspection{}, err
	}
	inspection.TenantHits = len(tenants)
	inspection.CloudContextHits = len(config.CloudContexts)

	orphans := make(map[string]*OrphanedAlias)
	addOrphanTenant := func(alias, tenant string) {
		alias = strings.TrimSpace(alias)
		tenant = strings.TrimSpace(tenant)
		if alias == "" || tenant == "" {
			return
		}
		entry := orphans[alias]
		if entry == nil {
			entry = newOrphanedAlias(alias)
			orphans[alias] = entry
		}
		if !containsTrimmedAlias(entry.ReferencedByTenants, tenant) {
			entry.ReferencedByTenants = append(entry.ReferencedByTenants, tenant)
		}
	}
	addOrphanContext := func(alias, contextName, region string) {
		alias = strings.TrimSpace(alias)
		contextName = strings.TrimSpace(contextName)
		if alias == "" || contextName == "" {
			return
		}
		entry := orphans[alias]
		if entry == nil {
			entry = newOrphanedAlias(alias)
			orphans[alias] = entry
		}
		ref := OrphanedAliasContextRef{Name: contextName, Region: strings.TrimSpace(region)}
		for _, existing := range entry.ReferencedByCloudContexts {
			if existing.Name == ref.Name {
				return
			}
		}
		entry.ReferencedByCloudContexts = append(entry.ReferencedByCloudContexts, ref)
	}

	for _, tenant := range tenants {
		aliases := append([]string{}, tenant.CloudProviderAliases...)
		if primary := strings.TrimSpace(tenant.PrimaryCloudProviderAlias); primary != "" {
			aliases = append(aliases, primary)
		}
		for _, alias := range aliases {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			if _, ok := configured[alias]; ok {
				continue
			}
			addOrphanTenant(alias, tenant.Name)
		}
	}

	for _, cloudContext := range config.CloudContexts {
		alias := strings.TrimSpace(cloudContext.CloudProviderAlias)
		if alias == "" {
			continue
		}
		if _, ok := configured[alias]; ok {
			continue
		}
		addOrphanContext(alias, cloudContext.Name, cloudContext.Region)
	}

	if len(orphans) == 0 {
		return inspection, nil
	}
	aliases := make([]string, 0, len(orphans))
	for alias := range orphans {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		entry := orphans[alias]
		sort.Strings(entry.ReferencedByTenants)
		sort.Slice(entry.ReferencedByCloudContexts, func(i, j int) bool {
			return entry.ReferencedByCloudContexts[i].Name < entry.ReferencedByCloudContexts[j].Name
		})
		inspection.OrphanedAliases = append(inspection.OrphanedAliases, *entry)
	}
	return inspection, nil
}

func newOrphanedAlias(alias string) *OrphanedAlias {
	entry := &OrphanedAlias{Alias: alias}
	if username, accountID, provider, ok := ParseCloudProviderAlias(alias); ok {
		entry.Username = username
		entry.AccountID = accountID
		entry.Provider = provider
		entry.Parsed = true
	}
	return entry
}

func containsTrimmedAlias(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

// RepairOrphanedAliasParams carries the fields needed to re-init a
// provider for one orphan. Region is sourced from a referenced cloud
// context when available; SSORegion / SSOStartURL come from the
// caller because they are not derivable from any persisted state.
type RepairOrphanedAliasParams struct {
	Orphan        OrphanedAlias
	SSORegion     string
	SSOStartURL   string
	RoleName      string
	Region        string
	OIDCIssuerURL string
	SkipLogin     bool
}

// RepairOrphanedAlias re-creates a CloudProviderConfig for one
// orphaned alias by routing to InitAWSCloudProvider. The
// implementation is intentionally a thin adapter: the heavy lifting
// (SSO setup, identity probe, OIDC issuer resolution) already lives
// in the cloud init flow, and replicating it here would fork the
// upstream contract. Returns an error for non-AWS providers (the
// only supported cloud today) or for unparseable aliases.
func RepairOrphanedAlias(ctx Context, store CloudStore, params RepairOrphanedAliasParams, deps CloudDependencies) (CloudProviderConfig, error) {
	if !params.Orphan.Parsed {
		return CloudProviderConfig{}, fmt.Errorf("orphaned alias %q cannot be parsed; recreate the provider manually with `erun cloud init aws`", params.Orphan.Alias)
	}
	if params.Orphan.Provider != CloudProviderAWS {
		return CloudProviderConfig{}, fmt.Errorf("unsupported provider %q for alias %q; only %q is supported", params.Orphan.Provider, params.Orphan.Alias, CloudProviderAWS)
	}
	initParams := InitAWSCloudProviderParams{
		Username:      params.Orphan.Username,
		AccountID:     params.Orphan.AccountID,
		SSORegion:     strings.TrimSpace(params.SSORegion),
		SSOStartURL:   strings.TrimSpace(params.SSOStartURL),
		RoleName:      strings.TrimSpace(params.RoleName),
		Region:        strings.TrimSpace(params.Region),
		OIDCIssuerURL: strings.TrimSpace(params.OIDCIssuerURL),
		SkipLogin:     params.SkipLogin,
	}
	return InitAWSCloudProvider(ctx, store, initParams, deps)
}
