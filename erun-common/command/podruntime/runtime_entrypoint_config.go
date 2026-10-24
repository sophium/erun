package podruntime

import (
	"os"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/orchestration"
	"github.com/sophium/erun/erun-common/primitive/shared"
	"gopkg.in/yaml.v3"
)

func describeRuntimeBootConfig(boot runtimeBoot) string {
	if !boot.hasTarget() {
		return ""
	}
	return "write the erun config for " + boot.tenant() + "/" + boot.environment() + " under " + filepath.Join(boot.configHome(), configpkg.ConfigRoot)
}

// writeRuntimeBootConfig writes the pod's erun config from the environment the
// chart injects: the root config's default tenant and infrastructure cloud
// provider/context, the tenant's default environment, and the environment's
// projection (type, contexts, registries, idle policy) plus its repository path.
// The projection is ResolveInjectedRuntimeConfig, the same one `erun doctor
// --sync-config` reconciles against, so a freshly booted pod is in sync by
// construction. Keys the injection does not carry are preserved, as every
// config writer preserves them.
func writeRuntimeBootConfig(_ core.Context, boot runtimeBoot, _ *runtimeSupervisor) error {
	injected, ok := shared.ResolveInjectedRuntimeConfig(boot.env)
	if !ok {
		return nil
	}
	configHome := boot.configHome()
	if err := writeRuntimeBootRootConfig(configHome, injected); err != nil {
		return err
	}
	if err := writeRuntimeBootTenantConfig(configHome, injected); err != nil {
		return err
	}
	env, _, err := orchestration.LoadRuntimeEnvConfigForSync(configHome, injected.Tenant, injected.Environment)
	if err != nil {
		env = configpkg.EnvConfig{}
	}
	env = shared.OverlayInjectedEnvConfig(env, injected.Env)
	env.LocalRepoPath = boot.repoDir()
	return writeRuntimeBootYAML(orchestration.RuntimeEnvConfigPath(configHome, injected.Tenant, injected.Environment), env)
}

func writeRuntimeBootRootConfig(configHome string, injected shared.InjectedRuntimeConfig) error {
	root, _, err := orchestration.LoadRuntimeRootConfigForSync(configHome)
	if err != nil {
		root = configpkg.ERunConfig{}
	}
	root.DefaultTenant = injected.Tenant
	for _, provider := range injected.Providers {
		root.CloudProviders = cloud.UpsertCloudProvider(root.CloudProviders, provider)
	}
	for _, context := range injected.Contexts {
		root.CloudContexts = cloud.UpsertCloudContext(root.CloudContexts, context)
	}
	return writeRuntimeBootYAML(orchestration.RuntimeRootConfigPath(configHome), root)
}

func writeRuntimeBootTenantConfig(configHome string, injected shared.InjectedRuntimeConfig) error {
	path := filepath.Join(configHome, configpkg.ConfigRoot, injected.Tenant, configpkg.ConfigFile)
	tenant := configpkg.TenantConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if yaml.Unmarshal(data, &tenant) != nil {
			tenant = configpkg.TenantConfig{}
		}
	}
	tenant.Name = injected.Tenant
	tenant.DefaultEnvironment = injected.Environment
	return writeRuntimeBootYAML(path, tenant)
}

// writeRuntimeBootYAML writes a config file, keeping keys this binary's types do
// not declare. A file that does not parse is replaced: the boot is where a pod's
// config is rebuilt from what the chart says it is.
func writeRuntimeBootYAML(path string, config any) error {
	existing, _ := os.ReadFile(path)
	var probe yaml.Node
	if yaml.Unmarshal(existing, &probe) != nil || strings.TrimSpace(string(existing)) == "" {
		existing = nil
	}
	data, err := configpkg.MarshalConfigPreservingUnknownFields(existing, config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return configpkg.WriteFileAtomic(path, data, configpkg.ConfigFilePerm)
}
