package eruncommon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// In-pod config reconciliation (`erun doctor --sync-config`, #548).
//
// The runtime pod's on-disk erun config is a projection of the env's
// EnvConfig, written by the entrypoint from the ERUN_* env vars the chart
// injects. `--sync-config` rebuilds the canonical intent from those same env
// vars (injected env wins) and rewrites the on-disk files to match — the
// inverse of laptop-side doctor, where the on-disk root config is
// authoritative. It is in-pod only (the CLI gates on IsInRuntimeEnvironment).
//
// Only the env-derived projection is reconciled; every key the injected env
// does not carry (sshd, claude, aitool, runtimeversion, repopath/localrepopath,
// …) is read-modify-write preserved, never compared, never rewritten.

// ConfigDriftKind classifies why an on-disk projected key disagrees with the
// injected env.
type ConfigDriftKind string

const (
	// ConfigDriftMissing: the injected env carries the key but the on-disk
	// config does not.
	ConfigDriftMissing ConfigDriftKind = "missing"
	// ConfigDriftWrong: both carry the key but the values differ.
	ConfigDriftWrong ConfigDriftKind = "wrong"
	// ConfigDriftLegacyKey: the on-disk config still carries a pre-#376 legacy
	// key (e.g. `remote:`) that the canonical projection replaces.
	ConfigDriftLegacyKey ConfigDriftKind = "legacy"
)

// ConfigDriftField is one reconciled key that disagrees between the on-disk
// config and the injected env.
type ConfigDriftField struct {
	Scope    string // "env" or "root"
	Key      string
	OnDisk   string
	Injected string
	Kind     ConfigDriftKind
}

// InjectedRuntimeConfig is the canonical config the chart-injected ERUN_* env
// vars describe — the Go twin of the entrypoint's initialize_erun_config.
type InjectedRuntimeConfig struct {
	Tenant      string
	Environment string
	Env         EnvConfig
	Providers   []CloudProviderConfig
	Contexts    []CloudContextConfig
}

// ConfigSyncInspection is the result of comparing the in-pod config against the
// injected env.
type ConfigSyncInspection struct {
	ConfigHome  string
	Tenant      string
	Environment string
	HasInjected bool
	Injected    InjectedRuntimeConfig
	Drift       []ConfigDriftField
}

// InSync reports whether the on-disk config already matches the injected env.
func (c ConfigSyncInspection) InSync() bool {
	return len(c.Drift) == 0
}

// ResolveInjectedRuntimeConfig builds the canonical config from the chart's
// ERUN_* env vars. It returns ok=false when ERUN_TENANT/ERUN_ENVIRONMENT are
// unset (the entrypoint's own guard), so the caller can report "nothing to
// reconcile". It maps ERUN_ENV_TYPE straight to the canonical EnvConfig.Type
// (never emitting the legacy `remote:` key) and parses the cloud-provider alias
// with ParseCloudProviderAlias instead of the entrypoint's shell heuristic.
func ResolveInjectedRuntimeConfig(env func(string) string) (InjectedRuntimeConfig, bool) {
	if env == nil {
		env = os.Getenv
	}
	get := func(key string) string { return strings.TrimSpace(env(key)) }

	tenant := get("ERUN_TENANT")
	environment := get("ERUN_ENVIRONMENT")
	if tenant == "" || environment == "" {
		return InjectedRuntimeConfig{}, false
	}

	envType := EnvironmentType(get("ERUN_ENV_TYPE"))
	if !envType.IsValid() {
		envType = legacyEnvTypeFromRemoteSnapshot(strings.EqualFold(get("ERUN_REPO_REMOTE"), "true"), nil)
	}
	kubernetesContext := get("ERUN_KUBERNETES_CONTEXT")
	if kubernetesContext == "" {
		kubernetesContext = "in-cluster"
	}
	provider := get("ERUN_CLOUD_PROVIDER")
	alias := get("ERUN_CLOUD_PROVIDER_ALIAS")
	region := get("ERUN_CLOUD_REGION")
	instanceID := get("ERUN_CLOUD_INSTANCE_ID")
	contextName := get("ERUN_CLOUD_CONTEXT_NAME")

	// managedcloud mirrors the entrypoint: the explicit ERUN_CLOUD_ENVIRONMENT
	// flag, or a remote env with a fully-resolved cloud provider/alias/region.
	managedCloud := strings.EqualFold(get("ERUN_CLOUD_ENVIRONMENT"), "true") ||
		(strings.EqualFold(get("ERUN_REPO_REMOTE"), "true") && provider != "" && alias != "" && region != "")

	injected := InjectedRuntimeConfig{
		Tenant:      tenant,
		Environment: environment,
		Env: EnvConfig{
			Name:               environment,
			Type:               envType,
			KubernetesContext:  kubernetesContext,
			CloudProviderAlias: alias,
			ManagedCloud:       managedCloud,
			RuntimeRegistry:    get("ERUN_RUNTIME_REGISTRY"),
			DisableBuildScript: strings.EqualFold(get("ERUN_DISABLE_BUILD_SCRIPT"), "true"),
			Idle:               injectedIdleConfig(get),
		},
	}
	if registries := parseInjectedContainerRegistries(env("ERUN_CONTAINER_REGISTRIES")); len(registries) > 0 {
		injected.Env.ContainerRegistries = registries
	}

	if provider != "" && alias != "" {
		username, accountID, _, _ := ParseCloudProviderAlias(alias)
		injected.Providers = []CloudProviderConfig{{
			Alias:     alias,
			Provider:  provider,
			Username:  username,
			AccountID: accountID,
		}}
		if region != "" {
			injected.Contexts = []CloudContextConfig{{
				Name:               contextName,
				Provider:           provider,
				CloudProviderAlias: alias,
				Region:             region,
				InstanceID:         instanceID,
				KubernetesContext:  kubernetesContext,
			}}
		}
	}
	return injected, true
}

func injectedIdleConfig(get func(string) string) EnvironmentIdleConfig {
	idle := EnvironmentIdleConfig{
		Timeout:      get("ERUN_IDLE_TIMEOUT"),
		WorkingHours: get("ERUN_IDLE_WORKING_HOURS"),
		Timezone:     get("ERUN_IDLE_TIMEZONE"),
	}
	if idle.Timeout == "" {
		idle.Timeout = "5m0s"
	}
	if idle.WorkingHours == "" {
		idle.WorkingHours = "08:00-20:00"
	}
	if bytes := strings.TrimSpace(get("ERUN_IDLE_TRAFFIC_BYTES")); bytes != "" && bytes != "0" {
		var parsed int64
		if _, err := fmt.Sscan(bytes, &parsed); err == nil {
			idle.IdleTrafficBytes = parsed
		}
	}
	return idle
}

func parseInjectedContainerRegistries(value string) ContainerRegistries {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var registries ContainerRegistries
	if err := json.Unmarshal([]byte(value), &registries); err != nil {
		return nil
	}
	return registries
}

// runtimeConfigPaths resolves the in-pod env and root config file paths under
// the given config home (XDG_CONFIG_HOME or $HOME/.config) without going
// through the xdg global, so the inspection and writer are testable in
// isolation.
func runtimeEnvConfigPath(configHome, tenant, environment string) string {
	return filepath.Join(configHome, configRoot, tenant, environment, configFile)
}

func runtimeRootConfigPath(configHome string) string {
	return filepath.Join(configHome, configRoot, configFile)
}

// InspectRuntimeConfigSync compares the in-pod config under configHome against
// the injected env. It decodes the env file twice: once into EnvConfig (for
// value comparison, which migrates legacy keys away) and once into a raw map
// (to detect a legacy `remote:` key still on disk that the struct decode would
// silently migrate). repopath/localrepopath are deliberately excluded from the
// comparison — the injected projection carries no LocalRepoPath, so comparing
// it would flag spurious drift on every legacy pod config.
func InspectRuntimeConfigSync(configHome string, env func(string) string) (ConfigSyncInspection, error) {
	injected, ok := ResolveInjectedRuntimeConfig(env)
	inspection := ConfigSyncInspection{
		ConfigHome:  configHome,
		Tenant:      injected.Tenant,
		Environment: injected.Environment,
		HasInjected: ok,
		Injected:    injected,
	}
	if !ok {
		return inspection, nil
	}

	onDiskEnv, rawEnv, err := loadRuntimeEnvConfigForSync(configHome, injected.Tenant, injected.Environment)
	if err != nil {
		return inspection, err
	}
	inspection.Drift = append(inspection.Drift, envConfigDrift(injected.Env, onDiskEnv, rawEnv)...)

	rootConfig, _, err := loadRuntimeRootConfigForSync(configHome)
	if err != nil {
		return inspection, err
	}
	inspection.Drift = append(inspection.Drift, rootConfigDrift(injected, rootConfig)...)

	return inspection, nil
}

func loadRuntimeEnvConfigForSync(configHome, tenant, environment string) (EnvConfig, map[string]any, error) {
	path := runtimeEnvConfigPath(configHome, tenant, environment)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return EnvConfig{}, map[string]any{}, nil
		}
		return EnvConfig{}, nil, err
	}
	var config EnvConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return EnvConfig{}, nil, err
	}
	raw := map[string]any{}
	_ = yaml.Unmarshal(data, &raw)
	return config, raw, nil
}

func loadRuntimeRootConfigForSync(configHome string) (ERunConfig, bool, error) {
	path := runtimeRootConfigPath(configHome)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ERunConfig{}, false, nil
		}
		return ERunConfig{}, false, err
	}
	var config ERunConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return ERunConfig{}, false, err
	}
	return config, true, nil
}

func envConfigDrift(injected, onDisk EnvConfig, raw map[string]any) []ConfigDriftField {
	var drift []ConfigDriftField
	add := func(key, onDiskVal, injectedVal string, kind ConfigDriftKind) {
		drift = append(drift, ConfigDriftField{Scope: "env", Key: key, OnDisk: onDiskVal, Injected: injectedVal, Kind: kind})
	}

	// type — flag a lingering legacy `remote:` key even when the migrated value
	// already matches, so the rewrite drops it.
	if _, hasLegacyRemote := raw["remote"]; hasLegacyRemote {
		add("type", "remote", string(injected.Type), ConfigDriftLegacyKey)
	} else if onDisk.Type != injected.Type {
		add("type", string(onDisk.Type), string(injected.Type), driftKind(string(onDisk.Type)))
	}

	if strings.TrimSpace(onDisk.KubernetesContext) != strings.TrimSpace(injected.KubernetesContext) {
		add("kubernetescontext", onDisk.KubernetesContext, injected.KubernetesContext, driftKind(onDisk.KubernetesContext))
	}
	if strings.TrimSpace(onDisk.CloudProviderAlias) != strings.TrimSpace(injected.CloudProviderAlias) {
		add("cloudprovideralias", onDisk.CloudProviderAlias, injected.CloudProviderAlias, driftKind(onDisk.CloudProviderAlias))
	}
	if onDisk.ManagedCloud != injected.ManagedCloud {
		add("managedcloud", formatBool(onDisk.ManagedCloud), formatBool(injected.ManagedCloud), ConfigDriftWrong)
	}
	if onDisk.Idle != injected.Idle {
		add("idle", "", "", driftKindForIdle(onDisk.Idle))
	}
	if strings.TrimSpace(onDisk.RuntimeRegistry) != strings.TrimSpace(injected.RuntimeRegistry) {
		add("runtimeregistry", onDisk.RuntimeRegistry, injected.RuntimeRegistry, driftKind(onDisk.RuntimeRegistry))
	}
	if !reflect.DeepEqual(normalizeRegistries(onDisk.ContainerRegistries), normalizeRegistries(injected.ContainerRegistries)) {
		kind := ConfigDriftWrong
		if onDisk.ContainerRegistries.IsZero() {
			kind = ConfigDriftMissing
		}
		add("containerregistries", "", "", kind)
	}
	if onDisk.DisableBuildScript != injected.DisableBuildScript {
		add("disablebuildscript", formatBool(onDisk.DisableBuildScript), formatBool(injected.DisableBuildScript), ConfigDriftWrong)
	}
	return drift
}

func rootConfigDrift(injected InjectedRuntimeConfig, onDisk ERunConfig) []ConfigDriftField {
	var drift []ConfigDriftField
	for _, provider := range injected.Providers {
		existing, found := findCloudProvider(onDisk.CloudProviders, provider.Alias)
		switch {
		case !found:
			drift = append(drift, ConfigDriftField{Scope: "root", Key: "cloudproviders", Injected: provider.Alias, Kind: ConfigDriftMissing})
		case !reflect.DeepEqual(existing, provider):
			drift = append(drift, ConfigDriftField{Scope: "root", Key: "cloudproviders", OnDisk: existing.Alias, Injected: provider.Alias, Kind: ConfigDriftWrong})
		}
	}
	for _, context := range injected.Contexts {
		existing, found := findCloudContextByName(onDisk.CloudContexts, context.Name)
		switch {
		case !found:
			drift = append(drift, ConfigDriftField{Scope: "root", Key: "cloudcontexts", Injected: context.Name, Kind: ConfigDriftMissing})
		case existing.Region != context.Region || existing.InstanceID != context.InstanceID || existing.CloudProviderAlias != context.CloudProviderAlias || existing.Provider != context.Provider:
			drift = append(drift, ConfigDriftField{Scope: "root", Key: "cloudcontexts", OnDisk: existing.Name, Injected: context.Name, Kind: ConfigDriftWrong})
		}
	}
	return drift
}

func findCloudProvider(providers []CloudProviderConfig, alias string) (CloudProviderConfig, bool) {
	for _, provider := range providers {
		if provider.Alias == alias {
			return provider, true
		}
	}
	return CloudProviderConfig{}, false
}

func findCloudContextByName(contexts []CloudContextConfig, name string) (CloudContextConfig, bool) {
	for _, context := range contexts {
		if context.Name == name {
			return context, true
		}
	}
	return CloudContextConfig{}, false
}

func normalizeRegistries(registries ContainerRegistries) ContainerRegistries {
	if len(registries) == 0 {
		return nil
	}
	return registries
}

func driftKind(onDiskValue string) ConfigDriftKind {
	if strings.TrimSpace(onDiskValue) == "" {
		return ConfigDriftMissing
	}
	return ConfigDriftWrong
}

func driftKindForIdle(onDisk EnvironmentIdleConfig) ConfigDriftKind {
	if onDisk == (EnvironmentIdleConfig{}) {
		return ConfigDriftMissing
	}
	return ConfigDriftWrong
}

func formatBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// RunRuntimeConfigSync rewrites the in-pod env and root config files so the
// env-derived projection matches the injected env, while preserving every key
// the injection does not carry. The write is load-overlay-save: it loads the
// on-disk struct (keeping unprojected keys), overlays only the projected
// fields, and marshals canonically — so a second run round-trips to InSync()
// with no perpetual-drift loop. Each file write is traced for the --dry-run
// contract and gated on !ctx.DryRun.
func RunRuntimeConfigSync(ctx Context, inspection ConfigSyncInspection) (ConfigSyncInspection, error) {
	if !inspection.HasInjected || inspection.InSync() {
		return inspection, nil
	}
	injected := inspection.Injected

	envPath := runtimeEnvConfigPath(inspection.ConfigHome, injected.Tenant, injected.Environment)
	onDiskEnv, _, err := loadRuntimeEnvConfigForSync(inspection.ConfigHome, injected.Tenant, injected.Environment)
	if err != nil {
		return inspection, err
	}
	reconciledEnv := overlayInjectedEnvConfig(onDiskEnv, injected.Env)
	if err := writeRuntimeYAML(ctx, envPath, reconciledEnv); err != nil {
		return inspection, err
	}

	if len(injected.Providers) > 0 || len(injected.Contexts) > 0 {
		rootPath := runtimeRootConfigPath(inspection.ConfigHome)
		rootConfig, _, err := loadRuntimeRootConfigForSync(inspection.ConfigHome)
		if err != nil {
			return inspection, err
		}
		if strings.TrimSpace(rootConfig.DefaultTenant) == "" {
			rootConfig.DefaultTenant = injected.Tenant
		}
		for _, provider := range injected.Providers {
			rootConfig.CloudProviders = upsertCloudProvider(rootConfig.CloudProviders, provider)
		}
		for _, context := range injected.Contexts {
			rootConfig.CloudContexts = upsertCloudContext(rootConfig.CloudContexts, context)
		}
		if !ctx.DryRun {
			_ = writeRootConfigBackupIfDue(rootPath, timeNow)
		}
		if err := writeRuntimeYAML(ctx, rootPath, rootConfig); err != nil {
			return inspection, err
		}
	}

	// Re-inspect so the returned inspection reflects the reconciled (or, in
	// dry-run, still-drifted) state.
	updated, err := InspectRuntimeConfigSync(inspection.ConfigHome, func(key string) string { return injectedEnvLookup(injected, key) })
	if err != nil {
		return inspection, err
	}
	return updated, nil
}

// overlayInjectedEnvConfig replaces only the env-projected fields on the
// on-disk config; all other fields (sshd, claude, aitool, runtimeversion,
// localRepoPath, …) are preserved as loaded.
func overlayInjectedEnvConfig(onDisk, injected EnvConfig) EnvConfig {
	onDisk.Name = injected.Name
	onDisk.Type = injected.Type
	onDisk.KubernetesContext = injected.KubernetesContext
	onDisk.CloudProviderAlias = injected.CloudProviderAlias
	onDisk.ManagedCloud = injected.ManagedCloud
	onDisk.Idle = injected.Idle
	onDisk.RuntimeRegistry = injected.RuntimeRegistry
	onDisk.ContainerRegistries = injected.ContainerRegistries
	onDisk.DisableBuildScript = injected.DisableBuildScript
	return onDisk
}

func writeRuntimeYAML(ctx Context, path string, config any) error {
	ctx.TraceCommand("", "write-yaml", path)
	if ctx.DryRun {
		return nil
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return ErrFailedToSaveConfig
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ErrNoUserDataFolder
	}
	return writeFileAtomic(path, data, 0o644)
}

// injectedEnvLookup re-derives the ERUN_* env values from a resolved injected
// config so the post-write re-inspection sees the same injected truth without
// re-reading os.Getenv (the test seam passes a custom env func to the first
// inspection; the re-inspection must use the same injected values).
func injectedEnvLookup(injected InjectedRuntimeConfig, key string) string {
	switch key {
	case "ERUN_TENANT":
		return injected.Tenant
	case "ERUN_ENVIRONMENT":
		return injected.Environment
	case "ERUN_ENV_TYPE":
		return string(injected.Env.Type)
	case "ERUN_KUBERNETES_CONTEXT":
		return injected.Env.KubernetesContext
	case "ERUN_CLOUD_PROVIDER_ALIAS":
		return injected.Env.CloudProviderAlias
	case "ERUN_CLOUD_ENVIRONMENT":
		return formatBool(injected.Env.ManagedCloud)
	case "ERUN_RUNTIME_REGISTRY":
		return injected.Env.RuntimeRegistry
	case "ERUN_DISABLE_BUILD_SCRIPT":
		return formatBool(injected.Env.DisableBuildScript)
	case "ERUN_CONTAINER_REGISTRIES":
		if len(injected.Env.ContainerRegistries) == 0 {
			return ""
		}
		encoded, err := json.Marshal(injected.Env.ContainerRegistries)
		if err != nil {
			return ""
		}
		return string(encoded)
	case "ERUN_IDLE_TIMEOUT":
		return injected.Env.Idle.Timeout
	case "ERUN_IDLE_WORKING_HOURS":
		return injected.Env.Idle.WorkingHours
	case "ERUN_IDLE_TIMEZONE":
		return injected.Env.Idle.Timezone
	}
	if len(injected.Contexts) > 0 {
		context := injected.Contexts[0]
		switch key {
		case "ERUN_CLOUD_PROVIDER":
			return context.Provider
		case "ERUN_CLOUD_REGION":
			return context.Region
		case "ERUN_CLOUD_INSTANCE_ID":
			return context.InstanceID
		case "ERUN_CLOUD_CONTEXT_NAME":
			return context.Name
		}
	} else if len(injected.Providers) > 0 {
		if key == "ERUN_CLOUD_PROVIDER" {
			return injected.Providers[0].Provider
		}
	}
	return ""
}
