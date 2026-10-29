package cloudstore

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
)

type CloudStore interface {
	configpkg.CloudReadStore
	SaveERunConfig(configpkg.ERunConfig) error
}

type EnvironmentCloudAliasStore interface {
	LoadEnvConfig(string, string) (configpkg.EnvConfig, string, error)
	SaveEnvConfig(string, configpkg.EnvConfig) error
}
