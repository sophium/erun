package shared

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
)

type stubCloudContextStore struct{ config configpkg.ERunConfig }

func (s stubCloudContextStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}
func (s stubCloudContextStore) SaveERunConfig(configpkg.ERunConfig) error { return nil }
