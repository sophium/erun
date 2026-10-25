package projectpaths

import (
	"errors"
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// ResolveProjectPath returns "" for an empty override, else joins a relative path
// to projectRoot.
func ResolveProjectPath(projectRoot, override string) string {
	override = strings.TrimSpace(override)
	if override == "" {
		return ""
	}
	if filepath.IsAbs(override) {
		return filepath.Clean(override)
	}
	return filepath.Join(filepath.Clean(strings.TrimSpace(projectRoot)), override)
}

// LoadProjectPaths reads path overrides; an uninitialized project has none, a
// corrupt config is an error.
func LoadProjectPaths(projectRoot string) (configpkg.ProjectPathsConfig, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return configpkg.ProjectPathsConfig{}, nil
	}
	config, _, err := configpkg.LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, configpkg.ErrNotInitialized) {
			return configpkg.ProjectPathsConfig{}, nil
		}
		return configpkg.ProjectPathsConfig{}, err
	}
	return config.Paths, nil
}
