package eruncommon

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adrg/xdg"
	"gopkg.in/yaml.v3"
)

const (
	configRoot       = "erun"
	configFile       = "config.yaml"
	projectConfigDir = ".erun"
)

type ERunConfig struct {
	DefaultTenant string
}

type TenantConfig struct {
	ProjectRoot        string
	Name               string
	DefaultEnvironment string
	Remote             bool  `yaml:"remote,omitempty"`
	Snapshot           *bool `yaml:"snapshot,omitempty"`
}

type EnvConfig struct {
	Name              string
	RepoPath          string
	KubernetesContext string
	ContainerRegistry string
	Remote            bool  `yaml:"remote,omitempty"`
	Snapshot          *bool `yaml:"snapshot,omitempty"`
}

func (c TenantConfig) SnapshotEnabled() bool {
	if c.Snapshot == nil {
		return true
	}
	return *c.Snapshot
}

func (c *TenantConfig) SetSnapshot(enabled bool) {
	if c == nil {
		return
	}
	value := enabled
	c.Snapshot = &value
}

func (c EnvConfig) SnapshotEnabled() bool {
	if c.Snapshot == nil {
		return true
	}
	return *c.Snapshot
}

func (c *EnvConfig) SetSnapshot(enabled bool) {
	if c == nil {
		return
	}
	value := enabled
	c.Snapshot = &value
}

type ProjectEnvironmentConfig struct {
	ContainerRegistry string `yaml:"containerregistry,omitempty"`
}

type ReleaseConfig struct {
	MainBranch    string `yaml:"mainbranch,omitempty"`
	DevelopBranch string `yaml:"developbranch,omitempty"`
}

type ProjectConfig struct {
	ContainerRegistry string                              `yaml:"containerregistry,omitempty"`
	Environments      map[string]ProjectEnvironmentConfig `yaml:"environments,omitempty"`
	Release           ReleaseConfig                       `yaml:"release,omitempty"`
}

func (c ProjectConfig) ContainerRegistryForEnvironment(environment string) string {
	environment = strings.TrimSpace(environment)
	if environment != "" && c.Environments != nil {
		if envConfig, ok := c.Environments[environment]; ok {
			if registry := strings.TrimSpace(envConfig.ContainerRegistry); registry != "" {
				return registry
			}
		}
	}

	return strings.TrimSpace(c.ContainerRegistry)
}

func (c *ProjectConfig) SetContainerRegistryForEnvironment(environment, registry string) {
	environment = strings.TrimSpace(environment)
	registry = strings.TrimSpace(registry)

	if environment == "" {
		c.ContainerRegistry = registry
		return
	}

	if registry == "" {
		if c.Environments != nil {
			delete(c.Environments, environment)
			if len(c.Environments) == 0 {
				c.Environments = nil
			}
		}
		return
	}

	if registry == strings.TrimSpace(c.ContainerRegistry) {
		if c.Environments != nil {
			delete(c.Environments, environment)
			if len(c.Environments) == 0 {
				c.Environments = nil
			}
		}
		return
	}

	if c.Environments == nil {
		c.Environments = make(map[string]ProjectEnvironmentConfig)
	}

	envConfig := c.Environments[environment]
	envConfig.ContainerRegistry = registry
	c.Environments[environment] = envConfig
}

func (c ProjectConfig) NormalizedReleaseConfig() ReleaseConfig {
	config := c.Release
	if strings.TrimSpace(config.MainBranch) == "" {
		config.MainBranch = DefaultReleaseMainBranch
	}
	if strings.TrimSpace(config.DevelopBranch) == "" {
		config.DevelopBranch = DefaultReleaseDevelopBranch
	}
	return config
}

var (
	ErrNotInitialized     = errors.New("not initialized")
	ErrNoUserDataFolder   = errors.New("failed to obtain config file locations")
	ErrConfigCorrupted    = errors.New("config file cannot be unmarshaled")
	ErrFailedToSaveConfig = errors.New("could not save struct to yaml file")
	ErrNotInGitRepository = errors.New("cannot find git project")
)

func ERunConfigDir() (string, error) {
	configHome := strings.TrimSpace(xdg.ConfigHome)
	if configHome == "" {
		return "", ErrNoUserDataFolder
	}
	return filepath.Join(configHome, configRoot), nil
}

type ConfigStore struct{}

func (ConfigStore) LoadERunConfig() (ERunConfig, string, error) {
	return LoadERunConfig()
}

func (ConfigStore) SaveERunConfig(config ERunConfig) error {
	return SaveERunConfig(config)
}

func (ConfigStore) ListTenantConfigs() ([]TenantConfig, error) {
	return ListTenantConfigs()
}

func (ConfigStore) LoadTenantConfig(tenant string) (TenantConfig, string, error) {
	return LoadTenantConfig(tenant)
}

func (ConfigStore) SaveTenantConfig(config TenantConfig) error {
	return SaveTenantConfig(config)
}

func (ConfigStore) LoadEnvConfig(tenant, envName string) (EnvConfig, string, error) {
	return LoadEnvConfig(tenant, envName)
}

func (ConfigStore) ResolveEffectiveKubernetesContext(environment, configured string) string {
	return resolveEffectiveKubernetesContext(environment, configured, listKubernetesContextNames, currentKubernetesContextName)
}

func (ConfigStore) ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	return ListEnvConfigs(tenant)
}

func (ConfigStore) SaveEnvConfig(tenant string, config EnvConfig) error {
	return SaveEnvConfig(tenant, config)
}

func SaveERunConfig(config ERunConfig) error {
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, configFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.MkdirAll(filepath.Dir(configFilePath), 0o755); err != nil {
		return ErrNoUserDataFolder
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return ErrFailedToSaveConfig
	}

	if err := os.WriteFile(configFilePath, data, 0o644); err != nil {
		return ErrFailedToSaveConfig
	}

	return nil
}

func LoadERunConfig() (ERunConfig, string, error) {
	config := ERunConfig{}
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, configFile))
	if err != nil {
		return config, configFilePath, ErrNoUserDataFolder
	}

	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return config, configFilePath, ErrNotInitialized
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, configFilePath, ErrConfigCorrupted
	}

	return config, configFilePath, nil
}

func SaveTenantConfig(config TenantConfig) error {
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, config.Name, configFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.MkdirAll(filepath.Dir(configFilePath), 0o755); err != nil {
		return ErrNoUserDataFolder
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return ErrFailedToSaveConfig
	}

	if err := os.WriteFile(configFilePath, data, 0o644); err != nil {
		return ErrFailedToSaveConfig
	}

	return nil
}

func LoadTenantConfig(tenant string) (TenantConfig, string, error) {
	config := TenantConfig{}
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, tenant, configFile))
	if err != nil {
		return config, configFilePath, ErrNoUserDataFolder
	}

	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return config, configFilePath, ErrNotInitialized
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, configFilePath, ErrConfigCorrupted
	}

	return config, configFilePath, nil
}

func ListTenantConfigs() ([]TenantConfig, error) {
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, configFile))
	if err != nil {
		return nil, ErrNoUserDataFolder
	}

	entries, err := os.ReadDir(filepath.Dir(configFilePath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	tenants := make([]TenantConfig, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		tenantConfig, _, err := LoadTenantConfig(entry.Name())
		if errors.Is(err, ErrNotInitialized) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if tenantConfig.Name == "" {
			tenantConfig.Name = entry.Name()
		}
		tenants = append(tenants, tenantConfig)
	}

	sort.Slice(tenants, func(i, j int) bool {
		return tenants[i].Name < tenants[j].Name
	})

	return tenants, nil
}

func SaveEnvConfig(tenant string, config EnvConfig) error {
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, tenant, config.Name, configFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.MkdirAll(filepath.Dir(configFilePath), 0o755); err != nil {
		return ErrNoUserDataFolder
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return ErrFailedToSaveConfig
	}

	if err := os.WriteFile(configFilePath, data, 0o644); err != nil {
		return ErrFailedToSaveConfig
	}

	return nil
}

func LoadEnvConfig(tenant, envName string) (EnvConfig, string, error) {
	config := EnvConfig{}
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, tenant, envName, configFile))
	if err != nil {
		return config, configFilePath, ErrNoUserDataFolder
	}

	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return config, configFilePath, ErrNotInitialized
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, configFilePath, ErrConfigCorrupted
	}

	return config, configFilePath, nil
}

func ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	configFilePath, err := xdg.ConfigFile(filepath.Join(configRoot, tenant, configFile))
	if err != nil {
		return nil, ErrNoUserDataFolder
	}

	entries, err := os.ReadDir(filepath.Dir(configFilePath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	envs := make([]EnvConfig, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		envConfig, _, err := LoadEnvConfig(tenant, entry.Name())
		if errors.Is(err, ErrNotInitialized) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if envConfig.Name == "" {
			envConfig.Name = entry.Name()
		}
		envs = append(envs, envConfig)
	}

	sort.Slice(envs, func(i, j int) bool {
		return envs[i].Name < envs[j].Name
	})

	return envs, nil
}

func SaveProjectConfig(projectRoot string, config ProjectConfig) error {
	configFilePath, err := projectConfigPath(projectRoot)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(configFilePath), 0o755); err != nil {
		return ErrFailedToSaveConfig
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return ErrFailedToSaveConfig
	}

	if err := os.WriteFile(configFilePath, data, 0o644); err != nil {
		return ErrFailedToSaveConfig
	}

	return nil
}

func LoadProjectConfig(projectRoot string) (ProjectConfig, string, error) {
	config := ProjectConfig{}
	configFilePath, err := projectConfigPath(projectRoot)
	if err != nil {
		return config, "", err
	}

	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return config, configFilePath, ErrNotInitialized
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, configFilePath, ErrConfigCorrupted
	}

	return config, configFilePath, nil
}

func FindProjectRoot() (string, string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	return FindProjectRootFromDir(dir)
}

func FindProjectRootFromDir(dir string) (string, string, error) {
	dir = filepath.Clean(dir)
	for {
		gitDir := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			repoName := filepath.Base(dir)
			return repoName, dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", ErrNotInGitRepository
		}
		dir = parent
	}
}

func projectConfigPath(projectRoot string) (string, error) {
	if projectRoot == "" {
		return "", ErrNotInGitRepository
	}
	return filepath.Join(filepath.Clean(projectRoot), projectConfigDir, configFile), nil
}
