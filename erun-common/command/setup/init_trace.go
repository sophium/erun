package setup

import (
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"gopkg.in/yaml.v3"
)

func TraceBootstrapStore(ctx core.Context, store BootstrapStore) BootstrapStore {
	if store == nil {
		store = configpkg.ConfigStore{}
	}
	return tracedBootstrapStore{
		BootstrapStore: store,
		ctx:            ctx,
	}
}

func TraceProjectConfigSaver(ctx core.Context, save ProjectConfigSaverFunc) ProjectConfigSaverFunc {
	if save == nil {
		save = configpkg.SaveProjectConfig
	}
	return func(projectRoot string, config configpkg.ProjectConfig) error {
		if strings.TrimSpace(projectRoot) == "" {
			return configpkg.ErrNotInGitRepository
		}
		configPath := filepath.Join(filepath.Clean(projectRoot), ".erun", "config.yaml")
		if err := traceYAMLWrite(ctx, configPath, config); err != nil {
			return err
		}
		if ctx.DryRun {
			return nil
		}
		return save(projectRoot, config)
	}
}

func TraceNamespaceEnsurer(ctx core.Context, ensure deploy.NamespaceEnsurerFunc) deploy.NamespaceEnsurerFunc {
	if ensure == nil {
		return nil
	}
	return func(contextName, namespace string) error {
		kube.TraceEnsureKubernetesNamespace(ctx, contextName, namespace)
		if ctx.DryRun {
			return nil
		}
		return ensure(contextName, namespace)
	}
}

func traceYAMLWrite(ctx core.Context, path string, value any) error {
	if _, err := yaml.Marshal(value); err != nil {
		return configpkg.ErrFailedToSaveConfig
	}
	ctx.TraceCommand("", "mkdir", "-p", filepath.Dir(path))
	ctx.TraceCommand("", "write-yaml", path)
	return nil
}

type tracedBootstrapStore struct {
	BootstrapStore
	ctx core.Context
}

func (s tracedBootstrapStore) SaveERunConfig(config configpkg.ERunConfig) error {
	configPath, err := configpkg.ResolveConfigFilePath(filepath.Join("erun", "config.yaml"))
	if err != nil {
		return configpkg.ErrNoUserDataFolder
	}
	if err := traceYAMLWrite(s.ctx, configPath, config); err != nil {
		return err
	}
	if s.ctx.DryRun {
		return nil
	}
	return s.BootstrapStore.SaveERunConfig(config)
}

func (s tracedBootstrapStore) SaveTenantConfig(config configpkg.TenantConfig) error {
	// Validate exactly as the real save does, so a dry run never traces a write the
	// real run refuses.
	config = configpkg.NormalizeTenantConfig(config)
	if err := configpkg.ValidateStatePathSegment("tenant", config.Name); err != nil {
		return err
	}
	configPath, err := configpkg.ResolveConfigFilePath(filepath.Join("erun", config.Name, "config.yaml"))
	if err != nil {
		return configpkg.ErrNoUserDataFolder
	}
	if err := traceYAMLWrite(s.ctx, configPath, config); err != nil {
		return err
	}
	if s.ctx.DryRun {
		return nil
	}
	return s.BootstrapStore.SaveTenantConfig(config)
}

func (s tracedBootstrapStore) SaveEnvConfig(tenant string, config configpkg.EnvConfig) error {
	if err := configpkg.ValidateStatePathSegment("tenant", tenant); err != nil {
		return err
	}
	if err := configpkg.ValidateStatePathSegment("environment", config.Name); err != nil {
		return err
	}
	configPath, err := configpkg.ResolveConfigFilePath(filepath.Join("erun", tenant, config.Name, "config.yaml"))
	if err != nil {
		return configpkg.ErrNoUserDataFolder
	}
	if err := traceYAMLWrite(s.ctx, configPath, config); err != nil {
		return err
	}
	if s.ctx.DryRun {
		return nil
	}
	return s.BootstrapStore.SaveEnvConfig(tenant, config)
}

func (s tracedBootstrapStore) ListEnvConfigs(tenant string) ([]configpkg.EnvConfig, error) {
	portStore, ok := s.BootstrapStore.(interface {
		ListEnvConfigs(string) ([]configpkg.EnvConfig, error)
	})
	if !ok {
		return nil, configpkg.ErrNotInitialized
	}
	return portStore.ListEnvConfigs(tenant)
}
