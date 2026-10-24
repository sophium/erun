package config

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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

	// Best-effort backup of the previous live file before overwrite: a backup
	// failure must not block the save, because the bug we guard against is data
	// loss and refusing to save when the backup dir is full would itself lose data.
	// Idempotent across repeated saves within one local day.
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

	// A zero-length or unparseable file is the residue of an interrupted
	// write, possibly one not even erun's own -- loadConfigFile retries it a
	// few times before giving up. Treating it as "successfully loaded empty
	// config" lets the next writer rebuild a fresh ERunConfig{} with only the
	// field it cares about, silently dropping every other section, so a read
	// that never recovers is surfaced as corruption instead, routing callers
	// into the doctor recovery path.
	if err := LoadConfigFile(configFilePath, &config); err != nil {
		return config, configFilePath, err
	}

	return config, configFilePath, nil
}

func SaveTenantConfig(config TenantConfig) error {
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

func NormalizeTenantConfig(config TenantConfig) TenantConfig {
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

func LoadTenantConfig(tenant string) (TenantConfig, string, error) {
	config := TenantConfig{}
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

func ListTenantConfigs() ([]TenantConfig, error) {
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

	// Best-effort backup of the previous live env config before overwrite, like
	// SaveERunConfig. Idempotent within one local day; it guards against a wrong
	// value (e.g. a type silently resolved to "runtime" on a remote-agent env)
	// being persisted with no way back. A backup failure must not block the save,
	// since the bug guarded against is data loss and refusing to save when the
	// backup dir is unwritable would be worse.
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

	// Project config is the repository .erun/config.yaml: often tracked and
	// shared, and carrying no credential, it keeps the ordinary 0644 mode.
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

// FindProjectRoot resolves the project root, preferring ERUN_REPO_PATH when it
// is set and names an existing directory. A sourceless runtime pod surfaces the
// image-baked release tree at that path (a symlink into /opt/erun/release, with
// no .git), so the cwd .git walk would fail there; honoring the env var lets
// in-pod commands like `erun terraform` resolve the tree the same way the MCP
// server does. The var is set only inside the pod, so laptop behavior (walk up
// from cwd to the nearest .git) is unchanged.
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
