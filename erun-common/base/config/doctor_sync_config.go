package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func RuntimeEnvConfigPath(configHome, tenant, environment string) string {
	return filepath.Join(configHome, ConfigRoot, tenant, environment, ConfigFile)
}

func RuntimeRootConfigPath(configHome string) string {
	return filepath.Join(configHome, ConfigRoot, ConfigFile)
}

func LoadRuntimeEnvConfigForSync(configHome, tenant, environment string) (EnvConfig, map[string]any, error) {
	path := RuntimeEnvConfigPath(configHome, tenant, environment)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return EnvConfig{}, map[string]any{}, nil
		}
		return EnvConfig{}, nil, err
	}
	var config EnvConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return EnvConfig{}, nil, err
	}
	raw := map[string]any{}
	_ = yaml.Unmarshal(data, &raw)
	return config, raw, nil
}

func LoadRuntimeRootConfigForSync(configHome string) (ERunConfig, bool, error) {
	path := RuntimeRootConfigPath(configHome)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ERunConfig{}, false, nil
		}
		return ERunConfig{}, false, err
	}
	var config ERunConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return ERunConfig{}, false, err
	}
	return config, true, nil
}

func FindCloudProvider(providers []CloudProviderConfig, alias string) (CloudProviderConfig, bool) {
	for _, provider := range providers {
		if provider.Alias == alias {
			return provider, true
		}
	}
	return CloudProviderConfig{}, false
}
