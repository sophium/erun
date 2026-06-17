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

	injected := InjectedRuntimeConfig{
		Tenant:      tenant,
		Environment: environment,
		Env: EnvConfig{
			Name:               environment,
			Type:               envType,
			KubernetesContext:  kubernetesContext,
			CloudProviderAlias: alias,
			ManagedCloud:       injectedManagedCloud(get, provider, alias, region),
			RuntimeRegistry:    get("ERUN_RUNTIME_REGISTRY"),
			DisableBuildScript: strings.EqualFold(get("ERUN_DISABLE_BUILD_SCRIPT"), "true"),
			Idle:               injectedIdleConfig(get),
		},
	}
	if registries := parseInjectedContainerRegistries(env("ERUN_CONTAINER_REGISTRIES")); len(registries) > 0 {
		injected.Env.ContainerRegistries = registries
	}
	injected.Providers, injected.Contexts = injectedCloudConfig(provider, alias, region, instanceID, contextName, kubernetesContext)
	return injected, true
}

// injectedCloudConfig builds the root cloud provider/context the env's cloud
// ERUN_* vars describe (empty when the env carries no cloud provider/alias).
func injectedCloudConfig(provider, alias, region, instanceID, contextName, kubernetesContext string) ([]CloudProviderConfig, []CloudContextConfig) {
	if provider == "" || alias == "" {
		return nil, nil
	}
	username, accountID, _, _ := ParseCloudProviderAlias(alias)
	providers := []CloudProviderConfig{{Alias: alias, Provider: provider, Username: username, AccountID: accountID}}
	if region == "" {
		return providers, nil
	}
	contexts := []CloudContextConfig{{
		Name:               contextName,
		Provider:           provider,
		CloudProviderAlias: alias,
		Region:             region,
		InstanceID:         instanceID,
		KubernetesContext:  kubernetesContext,
	}}
	return providers, contexts
}

// injectedManagedCloud mirrors the entrypoint's managedcloud computation: the
// explicit ERUN_CLOUD_ENVIRONMENT flag, or a remote env with a fully-resolved
// cloud provider/alias/region.
func injectedManagedCloud(get func(string) string, provider, alias, region string) bool {
	if strings.EqualFold(get("ERUN_CLOUD_ENVIRONMENT"), "true") {
		return true
	}
	if !strings.EqualFold(get("ERUN_REPO_REMOTE"), "true") {
		return false
	}
	return provider != "" && alias != "" && region != ""
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

// envTypeDrift reports drift on the env `type`, flagging a lingering legacy
// `remote:` key even when the migrated value already matches (so the rewrite
// drops it in favour of the canonical type).
func envTypeDrift(injected, onDisk EnvConfig, raw map[string]any) (ConfigDriftField, bool) {
	if _, hasLegacyRemote := raw["remote"]; hasLegacyRemote {
		return ConfigDriftField{Scope: "env", Key: "type", OnDisk: "remote", Injected: string(injected.Type), Kind: ConfigDriftLegacyKey}, true
	}
	if onDisk.Type != injected.Type {
		return ConfigDriftField{Scope: "env", Key: "type", OnDisk: string(onDisk.Type), Injected: string(injected.Type), Kind: driftKind(string(onDisk.Type))}, true
	}
	return ConfigDriftField{}, false
}

func envConfigDrift(injected, onDisk EnvConfig, raw map[string]any) []ConfigDriftField {
	var drift []ConfigDriftField
	add := func(key, onDiskVal, injectedVal string, kind ConfigDriftKind) {
		drift = append(drift, ConfigDriftField{Scope: "env", Key: key, OnDisk: onDiskVal, Injected: injectedVal, Kind: kind})
	}

	if field, ok := envTypeDrift(injected, onDisk, raw); ok {
		drift = append(drift, field)
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
func RunRuntimeConfigSync(ctx Context, inspection ConfigSyncInspection) error {
	if !inspection.HasInjected || inspection.InSync() {
		return nil
	}
	injected := inspection.Injected

	envPath := runtimeEnvConfigPath(inspection.ConfigHome, injected.Tenant, injected.Environment)
	onDiskEnv, _, err := loadRuntimeEnvConfigForSync(inspection.ConfigHome, injected.Tenant, injected.Environment)
	if err != nil {
		return err
	}
	if err := writeRuntimeYAML(ctx, envPath, overlayInjectedEnvConfig(onDiskEnv, injected.Env)); err != nil {
		return err
	}
	return reconcileRuntimeRootConfig(ctx, inspection.ConfigHome, injected)
}

// reconcileRuntimeRootConfig upserts the injected cloud provider/context into
// the root config (preserving any other entries) when the env carries them.
func reconcileRuntimeRootConfig(ctx Context, configHome string, injected InjectedRuntimeConfig) error {
	if len(injected.Providers) == 0 && len(injected.Contexts) == 0 {
		return nil
	}
	rootPath := runtimeRootConfigPath(configHome)
	rootConfig, _, err := loadRuntimeRootConfigForSync(configHome)
	if err != nil {
		return err
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
	return writeRuntimeYAML(ctx, rootPath, rootConfig)
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
