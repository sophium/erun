package podruntime

import (
	"os"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/shared"
	"gopkg.in/yaml.v3"
)

func describeRuntimeBootConfig(boot runtimeBoot) string {
	if !boot.hasTarget() {
		return ""
	}
	return "write the erun config for " + boot.tenant() + "/" + boot.environment() + " under " + filepath.Join(boot.configHome(), configpkg.ConfigRoot)
}

// writeRuntimeBootConfig writes ResolveInjectedRuntimeConfig's projection, the one
// `erun doctor --sync-config` checks, so a booted pod is in sync by construction.
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
	env, _, err := configpkg.LoadRuntimeEnvConfigForSync(configHome, injected.Tenant, injected.Environment)
	if err != nil {
		env = configpkg.EnvConfig{}
	}
	env = shared.OverlayInjectedEnvConfig(env, injected.Env)
	env.LocalRepoPath = boot.repoDir()
	return writeRuntimeBootYAML(configpkg.RuntimeEnvConfigPath(configHome, injected.Tenant, injected.Environment), env)
}

func writeRuntimeBootRootConfig(configHome string, injected shared.InjectedRuntimeConfig) error {
	root, _, err := configpkg.LoadRuntimeRootConfigForSync(configHome)
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
	return writeRuntimeBootYAML(configpkg.RuntimeRootConfigPath(configHome), root)
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

// writeRuntimeBootYAML keeps undeclared keys and replaces a file that does not parse.
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
