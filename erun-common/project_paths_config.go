package eruncommon

import (
	"errors"
	"path/filepath"
	"strings"
)

// ProjectPathsConfig overrides where erun discovers a project's devops assets,
// declared under `paths:` in a project's `.erun/config.yaml`. It is
// project-global (not per-environment). Every field is optional; an unset field
// keeps the conventional location: docker/ and k8s/ under <tenant>-devops/,
// Terraform roots at terraform-<tenant>/, and the VERSION file discovered by
// walking up from the build dir to the project root.
//
// A configured path resolves relative to the project root (an absolute path is
// honored as-is). The override relocates the canonical folders — the docker and
// k8s directories still carry their conventional names — so only where erun
// looks changes, not the layout inside those folders.
type ProjectPathsConfig struct {
	// Docker is the directory (named "docker") whose subdirectories are the
	// per-component build contexts. Default <tenant>-devops/docker.
	Docker string `yaml:"docker,omitempty"`
	// K8s is the directory (named "k8s") whose subdirectories are the
	// per-component Helm charts. Default <tenant>-devops/k8s.
	K8s string `yaml:"k8s,omitempty"`
	// Terraform is the base directory under which per-environment Terraform
	// roots live; erun still appends /<environment>. Default terraform-<tenant>.
	Terraform string `yaml:"terraform,omitempty"`
	// Version is the path to the VERSION file that mints the build version. A
	// directory is accepted and resolves to <dir>/VERSION.
	Version string `yaml:"version,omitempty"`
}

// IsZero reports whether no path override is set, i.e. every folder uses its
// conventional location.
func (c ProjectPathsConfig) IsZero() bool {
	return strings.TrimSpace(c.Docker) == "" &&
		strings.TrimSpace(c.K8s) == "" &&
		strings.TrimSpace(c.Terraform) == "" &&
		strings.TrimSpace(c.Version) == ""
}

// resolveProjectPath resolves a configured override against the project root.
// An empty override yields "" so callers fall back to the convention; a
// relative path is joined to projectRoot and an absolute path is used as-is.
func resolveProjectPath(projectRoot, override string) string {
	override = strings.TrimSpace(override)
	if override == "" {
		return ""
	}
	if filepath.IsAbs(override) {
		return filepath.Clean(override)
	}
	return filepath.Join(filepath.Clean(strings.TrimSpace(projectRoot)), override)
}

// loadProjectPaths reads a project's configured path overrides, tolerating an
// uninitialized project (no .erun/config.yaml) as "no overrides" so convention
// discovery still runs. A corrupt config surfaces as an error rather than being
// silently treated as unset.
func loadProjectPaths(projectRoot string) (ProjectPathsConfig, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return ProjectPathsConfig{}, nil
	}
	config, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return ProjectPathsConfig{}, nil
		}
		return ProjectPathsConfig{}, err
	}
	return config.Paths, nil
}
