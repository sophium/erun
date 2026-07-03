package eruncommon

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// RootConfigStatus is the doctor's first gate: a non-OK status means the
// root config file must be restored before alias-level repair makes sense.
type RootConfigStatus string

const (
	RootConfigStatusOK        RootConfigStatus = "ok"
	RootConfigStatusMissing   RootConfigStatus = "missing"
	RootConfigStatusCorrupted RootConfigStatus = "corrupted"
)

// OrphanedAliasContextRef names a cloud-context entry referencing a missing
// provider alias; Region is captured because it is the only context-side
// state a repair flow can reuse to seed provider init.
type OrphanedAliasContextRef struct {
	Name   string `json:"name"`
	Region string `json:"region"`
}

// OrphanedAlias collects every back-reference to a missing alias; when the
// alias cannot be decoded Parsed is false and the repair flow cannot
// auto-seed an init call.
type OrphanedAlias struct {
	Alias                     string                    `json:"alias"`
	Username                  string                    `json:"username,omitempty"`
	AccountID                 string                    `json:"accountId,omitempty"`
	Provider                  string                    `json:"provider,omitempty"`
	Parsed                    bool                      `json:"parsed"`
	ReferencedByTenants       []string                  `json:"referencedByTenants,omitempty"`
	ReferencedByCloudContexts []OrphanedAliasContextRef `json:"referencedByCloudContexts,omitempty"`
}

// RootConfigInspection is the read-only view of root-config state shared by
// the CLI and MCP doctors; nothing here performs side effects.
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

// OrphanedCloudContextEnvRef records a (tenant, env) pair referencing a
// missing cloud context, so doctor can map a broken context back to the
// env it affects.
type OrphanedCloudContextEnvRef struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
}

// OrphanedCloudContext aggregates every env reference to a missing cloud
// context; the coordinates decoded from its name let a recovery flow
// describe the missing instance without the user retyping anything.
type OrphanedCloudContext struct {
	KubernetesContext  string                       `json:"kubernetesContext"`
	AccountID          string                       `json:"accountId,omitempty"`
	Region             string                       `json:"region,omitempty"`
	CloudProviderAlias string                       `json:"cloudProviderAlias,omitempty"`
	ReferencedByEnvs   []OrphanedCloudContextEnvRef `json:"referencedByEnvs,omitempty"`
}

// Complete reports whether the root config is in a state the rest of erun
// can rely on.
func (r RootConfigInspection) Complete() bool {
	if r.ConfigStatus != RootConfigStatusOK {
		return false
	}
	if len(r.OrphanedAliases) != 0 {
		return false
	}
	return len(r.OrphanedContexts) == 0
}

// RootConfigInspectionStore is the read-only surface InspectRootConfig needs;
// it stays narrow so inspection still works when the central root config is
// unloadable, leaning on tenant/env files that load independently.
type RootConfigInspectionStore interface {
	LoadERunConfig() (ERunConfig, string, error)
	ListTenantConfigs() ([]TenantConfig, error)
	ListEnvConfigs(tenant string) ([]EnvConfig, error)
}

// InspectRootConfig walks the root and tenant configs to surface dangling
// cloud-provider-alias references without mutating anything; it also lists
// available backups so doctor can offer rollback even on a healthy install.
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

	// Without a cleanly loaded root there is no CloudProviders list to compare
	// against, so every alias would look orphaned; restore-from-backup is the
	// real fix in that state, surfaced via Backups.
	if inspection.ConfigStatus != RootConfigStatusOK {
		return inspection, nil
	}

	configured := configuredAliasSet(config.CloudProviders)
	inspection.ConfiguredCount = len(configured)

	tenants, err := store.ListTenantConfigs()
	if err != nil {
		return RootConfigInspection{}, err
	}
	inspection.TenantHits = len(tenants)
	inspection.CloudContextHits = len(config.CloudContexts)

	inspection.OrphanedAliases = detectOrphanedAliases(configured, tenants, config.CloudContexts)

	contextOrphans, err := collectOrphanedCloudContexts(store, config.CloudContexts, tenants)
	if err != nil {
		return RootConfigInspection{}, err
	}
	inspection.OrphanedContexts = contextOrphans

	return inspection, nil
}

func configuredAliasSet(providers []CloudProviderConfig) map[string]struct{} {
	configured := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		alias := strings.TrimSpace(provider.Alias)
		if alias == "" {
			continue
		}
		configured[alias] = struct{}{}
	}
	return configured
}

func detectOrphanedAliases(configured map[string]struct{}, tenants []TenantConfig, cloudContexts []CloudContextConfig) []OrphanedAlias {
	orphans := make(map[string]*OrphanedAlias)
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
			addOrphanTenantRef(orphans, alias, tenant.Name)
		}
	}
	for _, cloudContext := range cloudContexts {
		alias := strings.TrimSpace(cloudContext.CloudProviderAlias)
		if alias == "" {
			continue
		}
		if _, ok := configured[alias]; ok {
			continue
		}
		addOrphanContextRef(orphans, alias, cloudContext.Name, cloudContext.Region)
	}
	return sortedOrphanedAliases(orphans)
}

func addOrphanTenantRef(orphans map[string]*OrphanedAlias, alias, tenant string) {
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

func addOrphanContextRef(orphans map[string]*OrphanedAlias, alias, contextName, region string) {
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

func sortedOrphanedAliases(orphans map[string]*OrphanedAlias) []OrphanedAlias {
	if len(orphans) == 0 {
		return nil
	}
	aliases := make([]string, 0, len(orphans))
	for alias := range orphans {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	result := make([]OrphanedAlias, 0, len(aliases))
	for _, alias := range aliases {
		entry := orphans[alias]
		sort.Strings(entry.ReferencedByTenants)
		sort.Slice(entry.ReferencedByCloudContexts, func(i, j int) bool {
			return entry.ReferencedByCloudContexts[i].Name < entry.ReferencedByCloudContexts[j].Name
		})
		result = append(result, *entry)
	}
	return result
}

func collectOrphanedCloudContexts(store RootConfigInspectionStore, existing []CloudContextConfig, tenants []TenantConfig) ([]OrphanedCloudContext, error) {
	known := indexCloudContextsByKubernetesName(existing)
	orphans := make(map[string]*OrphanedCloudContext)
	for _, tenant := range tenants {
		envs, err := store.ListEnvConfigs(tenant.Name)
		if err != nil {
			return nil, err
		}
		for _, env := range envs {
			recordOrphanedCloudContext(orphans, known, tenant.Name, env)
		}
	}
	if len(orphans) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(orphans))
	for name := range orphans {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]OrphanedCloudContext, 0, len(names))
	for _, name := range names {
		entry := orphans[name]
		sort.Slice(entry.ReferencedByEnvs, func(i, j int) bool {
			if entry.ReferencedByEnvs[i].Tenant != entry.ReferencedByEnvs[j].Tenant {
				return entry.ReferencedByEnvs[i].Tenant < entry.ReferencedByEnvs[j].Tenant
			}
			return entry.ReferencedByEnvs[i].Environment < entry.ReferencedByEnvs[j].Environment
		})
		result = append(result, *entry)
	}
	return result, nil
}

func indexCloudContextsByKubernetesName(contexts []CloudContextConfig) map[string]struct{} {
	known := make(map[string]struct{}, 2*len(contexts))
	for _, c := range contexts {
		if name := strings.TrimSpace(c.Name); name != "" {
			known[name] = struct{}{}
		}
		if k := strings.TrimSpace(c.KubernetesContext); k != "" {
			known[k] = struct{}{}
		}
	}
	return known
}

func recordOrphanedCloudContext(orphans map[string]*OrphanedCloudContext, known map[string]struct{}, tenantName string, env EnvConfig) {
	if !envExpectsCloudContext(env) {
		return
	}
	kubeContext := strings.TrimSpace(env.KubernetesContext)
	if kubeContext == "" {
		return
	}
	if _, ok := known[kubeContext]; ok {
		return
	}
	entry := orphans[kubeContext]
	if entry == nil {
		account, region := parseErunCloudContextName(kubeContext)
		entry = &OrphanedCloudContext{
			KubernetesContext:  kubeContext,
			AccountID:          account,
			Region:             region,
			CloudProviderAlias: strings.TrimSpace(env.CloudProviderAlias),
		}
		orphans[kubeContext] = entry
	}
	ref := OrphanedCloudContextEnvRef{
		Tenant:      strings.TrimSpace(tenantName),
		Environment: strings.TrimSpace(env.Name),
	}
	for _, existing := range entry.ReferencedByEnvs {
		if existing == ref {
			return
		}
	}
	entry.ReferencedByEnvs = append(entry.ReferencedByEnvs, ref)
}

// envExpectsCloudContext gates orphan detection to erun-managed cloud envs;
// without it, every env pointing at a local kube target (orbstack, kind, ...)
// would look like an orphan whenever its context name is absent from
// CloudContexts.
func envExpectsCloudContext(env EnvConfig) bool {
	if strings.TrimSpace(env.CloudProviderAlias) != "" {
		return true
	}
	return env.ManagedCloud
}

// parseErunCloudContextName decodes erun's managed-context name
// "erun-<seq>-<accountid>-<region>", where region may contain hyphens
// (e.g. "eu-west-2"); a non-matching name yields ("", "") and is still
// recorded as an orphan, just without decoded coordinates.
func parseErunCloudContextName(name string) (string, string) {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, "erun-") {
		return "", ""
	}
	rest := strings.TrimPrefix(name, "erun-")
	parts := strings.SplitN(rest, "-", 3)
	if len(parts) < 3 {
		return "", ""
	}
	accountID := strings.TrimSpace(parts[1])
	region := strings.TrimSpace(parts[2])
	if len(accountID) != 12 {
		return "", ""
	}
	for _, c := range accountID {
		if c < '0' || c > '9' {
			return "", ""
		}
	}
	return accountID, region
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

// RepairOrphanedAlias re-creates a provider for one orphaned alias by
// delegating to InitAWSCloudProvider; it stays a thin adapter so the init
// flow's SSO/identity/OIDC handling is not forked here.
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
