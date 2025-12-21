package internal

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"gopkg.in/yaml.v3"
)

const (
	configRoot = "erun"
	configFile = "config.yaml"
)

type ERunConfig struct {
	DefaultTenant string
}

type TenantConfig struct {
	ProjectRoot        string
	Name               string
	DefaultEnvironment string
}

type EnvConfig struct {
	Name string
}

var (
	ErrNotInitialized     = errors.New("not initialized")
	ErrNoUserDataFolder   = errors.New("failed to obtain config file locations")
	ErrConfigCorrupted    = errors.New("config file cannot be unmarshaled")
	ErrFailedToSaveConfig = errors.New("could not save struct to yaml file")
	ErrNotInGitRepository = errors.New("cannot find git project")
)

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

func FindProjectRoot() (string, string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}

	for {
		gitDir := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
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
