package doctor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/sophium/erun/erun-common"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// In-pod config reconciliation (`erun doctor --sync-config`).
//
// The runtime pod's on-disk erun config is a projection of the env's
// EnvConfig, written by the runtime boot from the ERUN_* env vars the chart
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
	// ConfigDriftLegacyKey: the on-disk config still carries a legacy
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

// ConfigSyncInspection is the result of comparing the in-pod config against the
// injected env.
type ConfigSyncInspection struct {
	ConfigHome  string
	Tenant      string
	Environment string
	HasInjected bool
	Injected    eruncommon.InjectedRuntimeConfig
	Drift       []ConfigDriftField
}

// InSync reports whether the on-disk config already matches the injected env.
func (c ConfigSyncInspection) InSync() bool {
	return len(c.Drift) == 0
}

// ResolveRuntimeConfigHome follows the runtime boot's config-home precedence so
// every transport reads and writes the same tree the pod's config was written to.
func ResolveRuntimeConfigHome(homeDir string) string {
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		return configHome
	}
	return filepath.Join(homeDir, ".config")
}

// InspectRuntimeConfigSync compares the in-pod config under configHome against
// the injected env. It decodes the env file twice: once into EnvConfig (for
// value comparison, which migrates legacy keys away) and once into a raw map
// (to detect a legacy `remote:` key still on disk that the struct decode would
// silently migrate). repopath/localrepopath are deliberately excluded from the
// comparison — the injected projection carries no LocalRepoPath, so comparing
// it would flag spurious drift on every legacy pod config.
func InspectRuntimeConfigSync(configHome string, env func(string) string) (ConfigSyncInspection, error) {
	injected, ok := eruncommon.ResolveInjectedRuntimeConfig(env)
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

	onDiskEnv, rawEnv, err := eruncommon.LoadRuntimeEnvConfigForSync(configHome, injected.Tenant, injected.Environment)
	if err != nil {
		return inspection, err
	}
	inspection.Drift = append(inspection.Drift, envConfigDrift(injected.Env, onDiskEnv, rawEnv)...)

	rootConfig, _, err := eruncommon.LoadRuntimeRootConfigForSync(configHome)
	if err != nil {
		return inspection, err
	}
	inspection.Drift = append(inspection.Drift, rootConfigDrift(injected, rootConfig)...)

	return inspection, nil
}

// envTypeDrift reports drift on the env `type`, flagging a lingering legacy
// `remote:` key even when the migrated value already matches (so the rewrite
// drops it in favour of the canonical type).
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

func rootConfigDrift(injected eruncommon.InjectedRuntimeConfig, onDisk configpkg.ERunConfig) []ConfigDriftField {
	var drift []ConfigDriftField
	for _, provider := range injected.Providers {
		existing, found := eruncommon.FindCloudProvider(onDisk.CloudProviders, provider.Alias)
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

// RunRuntimeConfigSync rewrites the in-pod env and root config files so the
// env-derived projection matches the injected env, preserving every key the
// injection does not carry. Overlaying only the projected fields lets a second
// run round-trip to InSync() with no perpetual-drift loop.
func RunRuntimeConfigSync(ctx core.Context, inspection ConfigSyncInspection) error {
	if !inspection.HasInjected || inspection.InSync() {
		return nil
	}
	injected := inspection.Injected

	envPath := eruncommon.RuntimeEnvConfigPath(inspection.ConfigHome, injected.Tenant, injected.Environment)
	onDiskEnv, _, err := eruncommon.LoadRuntimeEnvConfigForSync(inspection.ConfigHome, injected.Tenant, injected.Environment)
	if err != nil {
		return err
	}
	// Back up before the overlay rewrites the file so an in-pod sync that
	// resolves a wrong value stays recoverable.
	if !ctx.DryRun {
		_ = configpkg.WriteEnvConfigBackupIfDue(envPath, configpkg.TimeNow)
	}
	if err := writeRuntimeYAML(ctx, envPath, eruncommon.OverlayInjectedEnvConfig(onDiskEnv, injected.Env)); err != nil {
		return err
	}
	return reconcileRuntimeRootConfig(ctx, inspection.ConfigHome, injected)
}

func reconcileRuntimeRootConfig(ctx core.Context, configHome string, injected eruncommon.InjectedRuntimeConfig) error {
	if len(injected.Providers) == 0 && len(injected.Contexts) == 0 {
		return nil
	}
	rootPath := eruncommon.RuntimeRootConfigPath(configHome)
	rootConfig, _, err := eruncommon.LoadRuntimeRootConfigForSync(configHome)
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
