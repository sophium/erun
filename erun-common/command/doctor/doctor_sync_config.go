package doctor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/primitive/shared/cloudconfig"
)

// ConfigSyncInspection is the result of comparing in-pod config with the injected env.
type ConfigSyncInspection struct {
	ConfigHome  string
	Tenant      string
	Environment string
	HasInjected bool
	Injected    cloudconfig.InjectedRuntimeConfig
	Drift       []model.ConfigDriftField
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
	injected, ok := cloudconfig.ResolveInjectedRuntimeConfig(env)
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
func envTypeDrift(injected, onDisk configpkg.EnvConfig, raw map[string]any) (model.ConfigDriftField, bool) {
	if _, hasLegacyRemote := raw["remote"]; hasLegacyRemote {
		return model.ConfigDriftField{Scope: "env", Key: "type", OnDisk: "remote", Injected: string(injected.Type), Kind: model.ConfigDriftLegacyKey}, true
	}
	if onDisk.Type != injected.Type {
		return model.ConfigDriftField{Scope: "env", Key: "type", OnDisk: string(onDisk.Type), Injected: string(injected.Type), Kind: driftKind(string(onDisk.Type))}, true
	}
	return model.ConfigDriftField{}, false
}

func envConfigDrift(injected, onDisk configpkg.EnvConfig, raw map[string]any) []model.ConfigDriftField {
	var drift []model.ConfigDriftField
	add := func(key, onDiskVal, injectedVal string, kind model.ConfigDriftKind) {
		drift = append(drift, model.ConfigDriftField{Scope: "env", Key: key, OnDisk: onDiskVal, Injected: injectedVal, Kind: kind})
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
		add("managedcloud", formatBool(onDisk.ManagedCloud), formatBool(injected.ManagedCloud), model.ConfigDriftWrong)
	}
	if onDisk.Idle != injected.Idle {
		add("idle", "", "", driftKindForIdle(onDisk.Idle))
	}
	if strings.TrimSpace(onDisk.RuntimeRegistry) != strings.TrimSpace(injected.RuntimeRegistry) {
		add("runtimeregistry", onDisk.RuntimeRegistry, injected.RuntimeRegistry, driftKind(onDisk.RuntimeRegistry))
	}
	if !reflect.DeepEqual(normalizeRegistries(onDisk.ContainerRegistries), normalizeRegistries(injected.ContainerRegistries)) {
		kind := model.ConfigDriftWrong
		if onDisk.ContainerRegistries.IsZero() {
			kind = model.ConfigDriftMissing
		}
		add("containerregistries", "", "", kind)
	}
	if onDisk.DisableBuildScript != injected.DisableBuildScript {
		add("disablebuildscript", formatBool(onDisk.DisableBuildScript), formatBool(injected.DisableBuildScript), model.ConfigDriftWrong)
	}
	return drift
}

func rootConfigDrift(injected cloudconfig.InjectedRuntimeConfig, onDisk configpkg.ERunConfig) []model.ConfigDriftField {
	var drift []model.ConfigDriftField
	for _, provider := range injected.Providers {
		existing, found := configpkg.FindCloudProvider(onDisk.CloudProviders, provider.Alias)
		switch {
		case !found:
			drift = append(drift, model.ConfigDriftField{Scope: "root", Key: "cloudproviders", Injected: provider.Alias, Kind: model.ConfigDriftMissing})
		case !reflect.DeepEqual(existing, provider):
			drift = append(drift, model.ConfigDriftField{Scope: "root", Key: "cloudproviders", OnDisk: existing.Alias, Injected: provider.Alias, Kind: model.ConfigDriftWrong})
		}
	}
	for _, context := range injected.Contexts {
		existing, found := findCloudContextByName(onDisk.CloudContexts, context.Name)
		switch {
		case !found:
			drift = append(drift, model.ConfigDriftField{Scope: "root", Key: "cloudcontexts", Injected: context.Name, Kind: model.ConfigDriftMissing})
		case existing.Region != context.Region || existing.InstanceID != context.InstanceID || existing.CloudProviderAlias != context.CloudProviderAlias || existing.Provider != context.Provider:
			drift = append(drift, model.ConfigDriftField{Scope: "root", Key: "cloudcontexts", OnDisk: existing.Name, Injected: context.Name, Kind: model.ConfigDriftWrong})
		}
	}
	return drift
}

func findCloudContextByName(contexts []model.CloudContextConfig, name string) (model.CloudContextConfig, bool) {
	for _, context := range contexts {
		if context.Name == name {
			return context, true
		}
	}
	return model.CloudContextConfig{}, false
}

func normalizeRegistries(registries configpkg.ContainerRegistries) configpkg.ContainerRegistries {
	if len(registries) == 0 {
		return nil
	}
	return registries
}

func driftKind(onDiskValue string) model.ConfigDriftKind {
	if strings.TrimSpace(onDiskValue) == "" {
		return model.ConfigDriftMissing
	}
	return model.ConfigDriftWrong
}

func driftKindForIdle(onDisk configpkg.EnvironmentIdleConfig) model.ConfigDriftKind {
	if onDisk == (configpkg.EnvironmentIdleConfig{}) {
		return model.ConfigDriftMissing
	}
	return model.ConfigDriftWrong
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
	if err := writeRuntimeYAML(ctx, envPath, cloudconfig.OverlayInjectedEnvConfig(onDiskEnv, injected.Env)); err != nil {
		return err
	}
	return reconcileRuntimeRootConfig(ctx, inspection.ConfigHome, injected)
}

func reconcileRuntimeRootConfig(ctx core.Context, configHome string, injected cloudconfig.InjectedRuntimeConfig) error {
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
		rootConfig.CloudProviders = providerconfig.UpsertCloudProvider(rootConfig.CloudProviders, provider)
	}
	for _, context := range injected.Contexts {
		rootConfig.CloudContexts = cloudcontext.UpsertCloudContext(rootConfig.CloudContexts, context)
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
