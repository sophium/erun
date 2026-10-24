package doctor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// `--sync-config` rewrites the in-pod config from the chart-injected ERUN_* env
// (env wins); keys the env does not carry are preserved untouched.

// ConfigDriftKind classifies why an on-disk key disagrees with the injected env.
type ConfigDriftKind string

const (
	// ConfigDriftMissing: the injected env carries the key but the disk does not.
	ConfigDriftMissing ConfigDriftKind = "missing"
	// ConfigDriftWrong: both carry the key with different values.
	ConfigDriftWrong ConfigDriftKind = "wrong"
	// ConfigDriftLegacyKey: the on-disk config still carries a legacy key such as `remote:`.
	ConfigDriftLegacyKey ConfigDriftKind = "legacy"
)

// ConfigDriftField is one key that disagrees between on-disk config and injected env.
type ConfigDriftField struct {
	Scope    string // "env" or "root"
	Key      string
	OnDisk   string
	Injected string
	Kind     ConfigDriftKind
}

// ConfigSyncInspection is the result of comparing in-pod config with the injected env.
type ConfigSyncInspection struct {
	ConfigHome  string
	Tenant      string
	Environment string
	HasInjected bool
	Injected    shared.InjectedRuntimeConfig
	Drift       []ConfigDriftField
}

// InSync reports whether the on-disk config matches the injected env.
func (c ConfigSyncInspection) InSync() bool {
	return len(c.Drift) == 0
}

// ResolveRuntimeConfigHome follows the runtime boot's config-home precedence.
func ResolveRuntimeConfigHome(homeDir string) string {
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		return configHome
	}
	return filepath.Join(homeDir, ".config")
}

// InspectRuntimeConfigSync compares the in-pod config with the injected env.
// The raw decode catches a legacy `remote:` key the struct decode migrates away;
// repopath is excluded because the injected projection has no LocalRepoPath.
func InspectRuntimeConfigSync(configHome string, env func(string) string) (ConfigSyncInspection, error) {
	injected, ok := shared.ResolveInjectedRuntimeConfig(env)
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

	onDiskEnv, rawEnv, err := configpkg.LoadRuntimeEnvConfigForSync(configHome, injected.Tenant, injected.Environment)
	if err != nil {
		return inspection, err
	}
	inspection.Drift = append(inspection.Drift, envConfigDrift(injected.Env, onDiskEnv, rawEnv)...)

	rootConfig, _, err := configpkg.LoadRuntimeRootConfigForSync(configHome)
	if err != nil {
		return inspection, err
	}
	inspection.Drift = append(inspection.Drift, rootConfigDrift(injected, rootConfig)...)

	return inspection, nil
}

// envTypeDrift flags a lingering legacy `remote:` key even when the value matches.
func envTypeDrift(injected, onDisk configpkg.EnvConfig, raw map[string]any) (ConfigDriftField, bool) {
	if _, hasLegacyRemote := raw["remote"]; hasLegacyRemote {
		return ConfigDriftField{Scope: "env", Key: "type", OnDisk: "remote", Injected: string(injected.Type), Kind: ConfigDriftLegacyKey}, true
	}
	if onDisk.Type != injected.Type {
		return ConfigDriftField{Scope: "env", Key: "type", OnDisk: string(onDisk.Type), Injected: string(injected.Type), Kind: driftKind(string(onDisk.Type))}, true
	}
	return ConfigDriftField{}, false
}

func envConfigDrift(injected, onDisk configpkg.EnvConfig, raw map[string]any) []ConfigDriftField {
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

func rootConfigDrift(injected shared.InjectedRuntimeConfig, onDisk configpkg.ERunConfig) []ConfigDriftField {
	var drift []ConfigDriftField
	for _, provider := range injected.Providers {
		existing, found := configpkg.FindCloudProvider(onDisk.CloudProviders, provider.Alias)
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

func findCloudContextByName(contexts []configpkg.CloudContextConfig, name string) (configpkg.CloudContextConfig, bool) {
	for _, context := range contexts {
		if context.Name == name {
			return context, true
		}
	}
	return configpkg.CloudContextConfig{}, false
}

func normalizeRegistries(registries configpkg.ContainerRegistries) configpkg.ContainerRegistries {
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

func driftKindForIdle(onDisk configpkg.EnvironmentIdleConfig) ConfigDriftKind {
	if onDisk == (configpkg.EnvironmentIdleConfig{}) {
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

// RunRuntimeConfigSync overlays only the projected fields, so a second run is InSync().
func RunRuntimeConfigSync(ctx core.Context, inspection ConfigSyncInspection) error {
	if !inspection.HasInjected || inspection.InSync() {
		return nil
	}
	injected := inspection.Injected

	envPath := configpkg.RuntimeEnvConfigPath(inspection.ConfigHome, injected.Tenant, injected.Environment)
	onDiskEnv, _, err := configpkg.LoadRuntimeEnvConfigForSync(inspection.ConfigHome, injected.Tenant, injected.Environment)
	if err != nil {
		return err
	}
	if !ctx.DryRun {
		_ = configpkg.WriteEnvConfigBackupIfDue(envPath, configpkg.TimeNow)
	}
	if err := writeRuntimeYAML(ctx, envPath, shared.OverlayInjectedEnvConfig(onDiskEnv, injected.Env)); err != nil {
		return err
	}
	return reconcileRuntimeRootConfig(ctx, inspection.ConfigHome, injected)
}

func reconcileRuntimeRootConfig(ctx core.Context, configHome string, injected shared.InjectedRuntimeConfig) error {
	if len(injected.Providers) == 0 && len(injected.Contexts) == 0 {
		return nil
	}
	rootPath := configpkg.RuntimeRootConfigPath(configHome)
	rootConfig, _, err := configpkg.LoadRuntimeRootConfigForSync(configHome)
	if err != nil {
		return err
	}
	if strings.TrimSpace(rootConfig.DefaultTenant) == "" {
		rootConfig.DefaultTenant = injected.Tenant
	}
	for _, provider := range injected.Providers {
		rootConfig.CloudProviders = cloud.UpsertCloudProvider(rootConfig.CloudProviders, provider)
	}
	for _, context := range injected.Contexts {
		rootConfig.CloudContexts = cloud.UpsertCloudContext(rootConfig.CloudContexts, context)
	}
	if !ctx.DryRun {
		_ = configpkg.WriteRootConfigBackupIfDue(rootPath, configpkg.TimeNow)
	}
	return writeRuntimeYAML(ctx, rootPath, rootConfig)
}

func writeRuntimeYAML(ctx core.Context, path string, config any) error {
	ctx.TraceCommand("", "write-yaml", path)
	if ctx.DryRun {
		return nil
	}
	existing, _ := os.ReadFile(path)
	data, err := configpkg.MarshalConfigPreservingUnknownFields(existing, config)
	if err != nil {
		return configpkg.ErrFailedToSaveConfig
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return configpkg.ErrNoUserDataFolder
	}
	return configpkg.WriteFileAtomic(path, data, configpkg.ConfigFilePerm)
}
