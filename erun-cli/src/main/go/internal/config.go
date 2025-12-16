package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"gopkg.in/yaml.v3"
)

// LoadERunConfig locates the erun config file via XDG and prints its contents.

type ERunConfig struct {
	Tenant string
}

type TenantConfig struct {
	Root string
}

type EnvConfig struct {
	Name string
}

var ErrNotInitialized = errors.New("not initialized")
var ErrNoUserDataFolder = errors.New("failed to obtain config file locations")
var ErrConfigCorrupted = errors.New("config file cannot be unmarshaled")
var ErrFailedToSaveConfig = errors.New("could not save struct to yaml file")

func SaveErunConfig(config ERunConfig) error {

	configFilePath, err := xdg.ConfigFile("erun/config.yaml")
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

func LoadERunConfig() (ERunConfig, error) {

	config := ERunConfig{}
	configFilePath, err := xdg.ConfigFile("erun/config.yaml")
	if err != nil {
		return config, ErrNoUserDataFolder
	}

	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return config, ErrNotInitialized
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, ErrConfigCorrupted
	}

	return config, nil
}

func SaveTenantConfig(config TenantConfig) error {

	configFilePath, err := xdg.ConfigFile(filepath.Join("erun", config.Root, "config.yaml"))
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

func LoadTenantConfig(root string) (TenantConfig, error) {

	config := TenantConfig{}
	configFilePath, err := xdg.ConfigFile(filepath.Join("erun", root, "config.yaml"))
	if err != nil {
		return config, ErrNoUserDataFolder
	}

	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return config, ErrNotInitialized
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, ErrConfigCorrupted
	}

	return config, nil
}

func SaveEnvConfig(tenantRoot string, config EnvConfig) error {

	configFilePath, err := xdg.ConfigFile(filepath.Join("erun", tenantRoot, config.Name, "config.yaml"))
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

func LoadEnvConfig(tenantRoot, envName string) (EnvConfig, error) {

	config := EnvConfig{}
	configFilePath, err := xdg.ConfigFile(filepath.Join("erun", tenantRoot, envName, "config.yaml"))
	if err != nil {
		return config, ErrNoUserDataFolder
	}

	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return config, ErrNotInitialized
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, ErrConfigCorrupted
	}

	return config, nil
}

func InitConfig() { //initializes configs and paths, setting defaults if not foind

	rootConfig, err := LoadERunConfig() // rootConfig is ERunConfig

	if errors.Is(err, ErrNotInitialized) {
		fmt.Println("ERun config not found, setting default")
		rootConfig = ERunConfig{Tenant: "default-tenant"}
	} else if err != nil {
		fmt.Println(err)
	}

	if err := SaveErunConfig(rootConfig); err != nil {
		fmt.Println(err)
	}

	tenantName := rootConfig.Tenant
	if tenantName == "" {
		tenantName = "default_tenant" //for now, user has to insert tenantName in the future
	}

	tenantConfig, err := LoadTenantConfig(tenantName)

	if errors.Is(err, ErrNotInitialized) {
		fmt.Println("Tenant config not found, setting default")
		tenantConfig = TenantConfig{Root: tenantName}
	} else if err != nil {
		fmt.Println(err)
	}

	if tenantConfig.Root == "" {
		tenantConfig.Root = tenantName
	}

	if err := SaveTenantConfig(tenantConfig); err != nil {
		fmt.Println(err)
	}

	envName := "default_env"
	envConfig, err := LoadEnvConfig(tenantName, envName)

	if errors.Is(err, ErrNotInitialized) {
		fmt.Println("Env config not found, setting default")
		envConfig = EnvConfig{Name: envName}
	} else if err != nil {
		fmt.Println(err)
	}

	if envConfig.Name == "" {
		envConfig.Name = envName
	}

	if err := SaveEnvConfig(tenantName, envConfig); err != nil {
		fmt.Println(err)
	}

}
