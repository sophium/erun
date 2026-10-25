package runtimesync

import (
	"os"
	"path/filepath"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"gopkg.in/yaml.v3"
)

func RuntimeEnvConfigPath(configHome, tenant, environment string) string {
	return filepath.Join(configHome, configpkg.ConfigRoot, tenant, environment, configpkg.ConfigFile)
}

func RuntimeRootConfigPath(configHome string) string {
	return filepath.Join(configHome, configpkg.ConfigRoot, configpkg.ConfigFile)
}

func LoadRuntimeEnvConfigForSync(configHome, tenant, environment string) (configpkg.EnvConfig, map[string]any, error) {
	path := RuntimeEnvConfigPath(configHome, tenant, environment)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return configpkg.EnvConfig{}, map[string]any{}, nil
		}
		return configpkg.EnvConfig{}, nil, err
	}
	var config configpkg.EnvConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return configpkg.EnvConfig{}, nil, err
	}
	raw := map[string]any{}
	_ = yaml.Unmarshal(data, &raw)
	return config, raw, nil
}

func LoadRuntimeRootConfigForSync(configHome string) (configpkg.ERunConfig, bool, error) {
	path := RuntimeRootConfigPath(configHome)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return configpkg.ERunConfig{}, false, nil
		}
		return configpkg.ERunConfig{}, false, err
	}
	var config configpkg.ERunConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return configpkg.ERunConfig{}, false, err
	}
	return config, true, nil
}

func FindCloudProvider(providers []model.CloudProviderConfig, alias string) (model.CloudProviderConfig, bool) {
	for _, provider := range providers {
		if provider.Alias == alias {
			return provider, true
		}
	}
	return model.CloudProviderConfig{}, false
}
