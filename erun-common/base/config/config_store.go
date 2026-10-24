package config

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

type ConfigStore struct{}

func (ConfigStore) LoadERunConfig() (ERunConfig, string, error) {
	return LoadERunConfig()
}

func (ConfigStore) SaveERunConfig(config ERunConfig) error {
	return SaveERunConfig(config)
}

func (ConfigStore) ListTenantConfigs() ([]model.TenantConfig, error) {
	return ListTenantConfigs()
}

func (ConfigStore) LoadTenantConfig(tenant string) (model.TenantConfig, string, error) {
	return LoadTenantConfig(tenant)
}

func (ConfigStore) SaveTenantConfig(config model.TenantConfig) error {
	return SaveTenantConfig(config)
}

func (ConfigStore) DeleteTenantConfig(tenant string) error {
	return DeleteTenantConfig(tenant)
}

func (ConfigStore) LoadEnvConfig(tenant, envName string) (EnvConfig, string, error) {
	return LoadEnvConfig(tenant, envName)
}

func (ConfigStore) ResolveEffectiveKubernetesContext(environment, configured string) string {
	return resolveEffectiveKubernetesContext(environment, configured, listKubernetesContextNames, currentKubernetesContextName)
}

func (ConfigStore) ResolveDeployKubernetesContext(environment, configured string) string {
	return resolveDeployKubernetesContext(environment, configured, currentKubernetesContextName)
}

func (ConfigStore) ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	return ListEnvConfigs(tenant)
}

func (ConfigStore) SaveEnvConfig(tenant string, config EnvConfig) error {
	return SaveEnvConfig(tenant, config)
}

func (ConfigStore) DeleteEnvConfig(tenant, envName string) error {
	return DeleteEnvConfig(tenant, envName)
}

func (ConfigStore) LoadProjectConfig(projectRoot string) (ProjectConfig, string, error) {
	return LoadProjectConfig(projectRoot)
}

func (ConfigStore) SaveProjectConfig(projectRoot string, config ProjectConfig) error {
	return SaveProjectConfig(projectRoot, config)
}

func SaveERunConfig(config ERunConfig) error {
	configFilePath, err := ResolveConfigFilePath(filepath.Join(ConfigRoot, ConfigFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.MkdirAll(filepath.Dir(configFilePath), 0o755); err != nil {
		return ErrNoUserDataFolder
	}

	existing, _ := os.ReadFile(configFilePath)
	data, err := MarshalConfigPreservingUnknownFields(existing, config)
	if err != nil {
		return ErrFailedToSaveConfig
	}

	// Best-effort daily backup; a backup failure must not block the save.
	_ = WriteRootConfigBackupIfDue(configFilePath, TimeNow)

	if err := WriteFileAtomic(configFilePath, data, ConfigFilePerm); err != nil {
		return ErrFailedToSaveConfig
	}

	return nil
}

func LoadERunConfig() (ERunConfig, string, error) {
	config := ERunConfig{}
	configFilePath, err := ResolveConfigFilePath(filepath.Join(ConfigRoot, ConfigFile))
	if err != nil {
		return config, configFilePath, ErrNoUserDataFolder
	}

	// An empty or unparseable file is an interrupted write; treating it as empty
	// would let the next writer drop every other section, so report corruption.
	if err := LoadConfigFile(configFilePath, &config); err != nil {
		return config, configFilePath, err
	}

	return config, configFilePath, nil
}

func SaveTenantConfig(config model.TenantConfig) error {
	config = NormalizeTenantConfig(config)
	if err := ValidateStatePathSegment("tenant", config.Name); err != nil {
		return err
	}
	configFilePath, err := ResolveConfigFilePath(filepath.Join(ConfigRoot, config.Name, ConfigFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.MkdirAll(filepath.Dir(configFilePath), 0o755); err != nil {
		return ErrNoUserDataFolder
	}

	existing, _ := os.ReadFile(configFilePath)
	data, err := MarshalConfigPreservingUnknownFields(existing, config)
	if err != nil {
		return ErrFailedToSaveConfig
	}

	if err := WriteFileAtomic(configFilePath, data, ConfigFilePerm); err != nil {
		return ErrFailedToSaveConfig
	}

	return nil
}

func NormalizeTenantConfig(config model.TenantConfig) model.TenantConfig {
	config.Name = strings.TrimSpace(config.Name)
	config.DefaultEnvironment = strings.TrimSpace(config.DefaultEnvironment)
	config.APIURL = strings.TrimSpace(config.APIURL)
	config.CloudProviderAliases, config.PrimaryCloudProviderAlias = NormalizeTenantCloudProviderAliases(config.CloudProviderAliases, config.PrimaryCloudProviderAlias)
	return config
}

func DeleteTenantConfig(tenant string) error {
	if err := ValidateStatePathSegment("tenant", tenant); err != nil {
		return err
	}
	configFilePath, err := ResolveConfigFilePath(filepath.Join(ConfigRoot, tenant, ConfigFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.RemoveAll(filepath.Dir(configFilePath)); err != nil {
		return ErrFailedToSaveConfig
	}
	return nil
}

func LoadTenantConfig(tenant string) (model.TenantConfig, string, error) {
	config := model.TenantConfig{}
	if err := validateReadableStatePathSegment("tenant", tenant); err != nil {
		return config, "", err
	}
	configFilePath, err := ResolveConfigFilePath(filepath.Join(ConfigRoot, tenant, ConfigFile))
	if err != nil {
		return config, configFilePath, ErrNoUserDataFolder
	}

	if err := LoadConfigFile(configFilePath, &config); err != nil {
		return config, configFilePath, err
	}

	return NormalizeTenantConfig(config), configFilePath, nil
}

func ListTenantConfigs() ([]model.TenantConfig, error) {
	configFilePath, err := ResolveConfigFilePath(filepath.Join(ConfigRoot, ConfigFile))
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

	tenants := make([]model.TenantConfig, 0, len(entries))
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
	if err := ValidateStatePathSegment("tenant", tenant); err != nil {
		return err
	}
	if err := ValidateStatePathSegment("environment", config.Name); err != nil {
		return err
	}
	configFilePath, err := ResolveConfigFilePath(filepath.Join(ConfigRoot, tenant, config.Name, ConfigFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.MkdirAll(filepath.Dir(configFilePath), 0o755); err != nil {
		return ErrNoUserDataFolder
	}

	existing, _ := os.ReadFile(configFilePath)
	data, err := MarshalConfigPreservingUnknownFields(existing, config)
	if err != nil {
		return ErrFailedToSaveConfig
	}

	// Best-effort daily backup, as in SaveERunConfig.
	_ = WriteEnvConfigBackupIfDue(configFilePath, TimeNow)

	if err := WriteFileAtomic(configFilePath, data, ConfigFilePerm); err != nil {
		return ErrFailedToSaveConfig
	}

	return nil
}

func DeleteEnvConfig(tenant, envName string) error {
	if err := ValidateStatePathSegment("tenant", tenant); err != nil {
		return err
	}
	if err := ValidateStatePathSegment("environment", envName); err != nil {
		return err
	}
	configFilePath, err := ResolveConfigFilePath(filepath.Join(ConfigRoot, tenant, envName, ConfigFile))
	if err != nil {
		return ErrNoUserDataFolder
	}

	if err := os.RemoveAll(filepath.Dir(configFilePath)); err != nil {
		return ErrFailedToSaveConfig
	}
	return nil
}

func LoadEnvConfig(tenant, envName string) (EnvConfig, string, error) {
	config := EnvConfig{}
	if err := validateReadableStatePathSegment("tenant", tenant); err != nil {
		return config, "", err
	}
	if err := validateReadableStatePathSegment("environment", envName); err != nil {
		return config, "", err
	}
	configFilePath, err := ResolveConfigFilePath(filepath.Join(ConfigRoot, tenant, envName, ConfigFile))
	if err != nil {
		return config, configFilePath, ErrNoUserDataFolder
	}

	if err := LoadConfigFile(configFilePath, &config); err != nil {
		return config, configFilePath, err
	}

	return config, configFilePath, nil
}

func ListEnvConfigs(tenant string) ([]EnvConfig, error) {
	configFilePath, err := ResolveConfigFilePath(filepath.Join(ConfigRoot, tenant, ConfigFile))
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

	existing, _ := os.ReadFile(configFilePath)
	data, err := MarshalConfigPreservingUnknownFields(existing, config)
	if err != nil {
		return ErrFailedToSaveConfig
	}

	// Project config is often tracked and holds no credential, so 0644.
	if err := WriteFileAtomic(configFilePath, data, 0o644); err != nil {
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

	if err := LoadConfigFile(configFilePath, &config); err != nil {
		return config, configFilePath, err
	}

	return config, configFilePath, nil
}

// FindProjectRoot prefers ERUN_REPO_PATH when it names a directory: a sourceless
// runtime pod has the release tree there with no .git to walk up to.
func FindProjectRoot() (string, string, error) {
	if root := strings.TrimSpace(os.Getenv("ERUN_REPO_PATH")); root != "" {
		root = filepath.Clean(root)
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return filepath.Base(root), root, nil
		}
	}
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
	return filepath.Join(filepath.Clean(projectRoot), ProjectConfigDir, ConfigFile), nil
}
