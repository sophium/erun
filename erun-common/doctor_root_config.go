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
	ConfigPath       string                 `json:"configPath"`
	ConfigStatus     RootConfigStatus       `json:"configStatus"`
	ConfigError      string                 `json:"configError,omitempty"`
	OrphanedAliases  []OrphanedAlias        `json:"orphanedAliases,omitempty"`
	OrphanedContexts []OrphanedCloudContext `json:"orphanedCloudContexts,omitempty"`
	Backups          []RootConfigBackup     `json:"backups,omitempty"`
	ConfiguredCount  int                    `json:"configuredProviderCount"`
	CloudContextHits int                    `json:"cloudContextCount"`
	TenantHits       int                    `json:"tenantCount"`
}

// OrphanedCloudContextEnvRef records one (tenant, env) pair whose
// EnvConfig.KubernetesContext names a cloud context that is missing
// from the root config's CloudContexts list. The pair is what
// doctor renders in its report so users can map "broken context"
// back to "which env am I supposed to open?".
type OrphanedCloudContextEnvRef struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
}

// OrphanedCloudContext aggregates every env reference for a single
// missing cloud-context name and the cloud coordinates parseable
// from the name itself (erun's naming convention encodes account ID
// and region). The decoded fields exist so a future recovery flow
// can describe the missing instance to AWS without making the user
// retype anything.
type OrphanedCloudContext struct {
	KubernetesContext  string                       `json:"kubernetesContext"`
	AccountID          string                       `json:"accountId,omitempty"`
	Region             string                       `json:"region,omitempty"`
	CloudProviderAlias string                       `json:"cloudProviderAlias,omitempty"`
	ReferencedByEnvs   []OrphanedCloudContextEnvRef `json:"referencedByEnvs,omitempty"`
}

// Complete reports whether the root config is in a state the rest of
// erun can rely on. False when the file failed to load OR when any
// orphan reference was found.
func (r RootConfigInspection) Complete() bool {
	if r.ConfigStatus != RootConfigStatusOK {
		return false
	}
	if len(r.OrphanedAliases) != 0 {
		return false
	}
	return len(r.OrphanedContexts) == 0
}

// RootConfigInspectionStore is the read-only surface InspectRootConfig
// needs. Kept narrow on purpose: the inspection must work even when
// the root config is unloadable, so it leans on ListTenantConfigs +
// ListEnvConfigs (which read tenant/env-level files independently)
// and a direct LoadERunConfig probe for the central file.
type RootConfigInspectionStore interface {
	LoadERunConfig() (ERunConfig, string, error)
	ListTenantConfigs() ([]TenantConfig, error)
	ListEnvConfigs(tenant string) ([]EnvConfig, error)
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

// configuredAliasSet returns the set of non-empty, trimmed cloud-provider
// aliases declared in the root config — the source of truth orphan detection
// compares tenant and cloud-context references against.
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

// detectOrphanedAliases finds cloud-provider aliases referenced by tenants or
// cloud contexts that are not present in the configured set, aggregated by
// alias and returned in a stable sorted order.
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

// addOrphanTenantRef records that tenant references the orphaned alias,
// de-duplicating tenant names.
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

// addOrphanContextRef records that the named cloud context references the
// orphaned alias, de-duplicating by context name.
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

// sortedOrphanedAliases flattens the orphan map into a slice ordered by alias,
// with each entry's tenant and cloud-context references sorted too, so the
// inspection output is deterministic.
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

// collectOrphanedCloudContexts walks every env config and reports
// kubernetescontext references that name a cloud-managed context
// (the env carries a CloudProviderAlias) but no matching
// CloudContextConfig exists in the root config. Local Kubernetes
// targets (orbstack, docker-desktop, kind, ...) are skipped because
// they are not erun-managed cloud contexts; the heuristic for
// "cloud-managed" is "env has a non-empty CloudProviderAlias OR the
// env is flagged ManagedCloud". Aggregates duplicate references so
// a context shared by N envs becomes a single entry with N back-refs.
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

// envExpectsCloudContext is the heuristic that separates local
// kubernetes targets (orbstack, kind, ...) from erun-managed cloud
// contexts: a non-empty CloudProviderAlias or ManagedCloud=true is
// the signal that the env is supposed to be backed by a managed
// CloudContextConfig. Without this gate every env on the box would
// look like an orphan reference whenever a local kube context name
// did not appear in CloudContexts.
func envExpectsCloudContext(env EnvConfig) bool {
	if strings.TrimSpace(env.CloudProviderAlias) != "" {
		return true
	}
	return env.ManagedCloud
}

// parseErunCloudContextName recognises erun's own naming convention
// for managed cloud contexts: "erun-<seq>-<accountid>-<region>" where
// <region> may itself contain hyphens (e.g. "eu-west-2"). The
// account ID is a 12-digit AWS identifier. Returns ("", "") for any
// name that does not match — the caller still records the orphan,
// just without decoded coordinates the recovery flow could lean on.
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
