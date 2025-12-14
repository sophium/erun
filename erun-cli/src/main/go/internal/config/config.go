package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const configDirName = ".erun"

// Config represents the persisted erun configuration file structure.
type Config struct {
	Home string `yaml:"home"`
}

// Loader resolves and loads a tenant configuration file, creating it when missing.
type Loader struct {
	// Tenant identifies the configuration namespace. It becomes the file name when Path is empty.
	Tenant string

	// Path overrides the configuration file location entirely when set.
	Path string
}

// Load reads the configuration from disk, ensuring the file exists and includes the required fields.
func (l Loader) Load() (*Config, string, error) {
	cfgPath, err := l.resolvePath()
	if err != nil {
		return nil, "", fmt.Errorf("resolve config path: %w", err)
	}

	cfgDir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return nil, "", fmt.Errorf("ensure config directory %s: %w", cfgDir, err)
	}

	cfg := &Config{}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.Home = cfgDir
			if err := writeConfig(cfgPath, cfg); err != nil {
				return nil, "", err
			}
			return cfg, cfgPath, nil
		}
		return nil, "", fmt.Errorf("read config file %s: %w", cfgPath, err)
	}

	if len(data) == 0 {
		cfg.Home = cfgDir
		if err := writeConfig(cfgPath, cfg); err != nil {
			return nil, "", err
		}
		return cfg, cfgPath, nil
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, "", fmt.Errorf("parse config file %s: %w", cfgPath, err)
	}

	if cfg.Home == "" {
		cfg.Home = cfgDir
		if err := writeConfig(cfgPath, cfg); err != nil {
			return nil, "", err
		}
	}

	return cfg, cfgPath, nil
}

func (l Loader) resolvePath() (string, error) {
	if l.Path != "" {
		return l.Path, nil
	}
	if l.Tenant == "" {
		return "", errors.New("tenant not specified")
	}

	homeDir, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}

	return filepath.Join(homeDir, configDirName, l.Tenant+".yaml"), nil
}

func userHomeDir() (string, error) {
	if home := os.Getenv("HOME"); home != "" {
		return home, nil
	}

	// Windows sets USERPROFILE while *nix systems typically use HOME.
	if profile := os.Getenv("USERPROFILE"); profile != "" {
		return profile, nil
	}

	homeDrive := os.Getenv("HOMEDRIVE")
	homePath := os.Getenv("HOMEPATH")
	if homeDrive != "" && homePath != "" {
		return filepath.Join(homeDrive, homePath), nil
	}

	return os.UserHomeDir()
}

func writeConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config file %s: %w", path, err)
	}

	return nil
}
