package repopath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

// HostRepoPathRequirement names why envType needs a host directory, shared by every refusal.
func HostRepoPathRequirement(envType config.EnvironmentType) string {
	if envType == config.EnvironmentTypeHost {
		return "it needs a host directory to use — run init from the project directory or pass --project-root"
	}
	return "it needs a host repo path to mount — run init from the project directory or pass --project-root"
}

// RequiresHostRepoPath reports whether envType's repo path must exist on this
// machine: local-agent mounts it and host is it; remote and runtime use a PVC.
func RequiresHostRepoPath(envType config.EnvironmentType) bool {
	return envType == config.EnvironmentTypeLocalAgent || envType == config.EnvironmentTypeHost
}

// ValidateEnvRepoPath validates the path only for types that require a host path.
func ValidateEnvRepoPath(envType config.EnvironmentType, path string) error {
	if !RequiresHostRepoPath(envType) {
		return nil
	}
	return ValidateHostRepoPath(envType, path)
}

// ValidateHostRepoPath requires an existing absolute directory, so a bad path is
// refused at edit time rather than failing later as a mount or deploy error.
func ValidateHostRepoPath(envType config.EnvironmentType, path string) error {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return fmt.Errorf("cannot use an empty repository path: %s", HostRepoPathRequirement(envType))
	}
	if !filepath.IsAbs(trimmed) {
		return fmt.Errorf("repository path %q is not absolute: %s", trimmed, HostRepoPathRequirement(envType))
	}
	info, err := os.Stat(trimmed)
	if err != nil {
		return fmt.Errorf("repository path %q is not a directory on this machine: %s", trimmed, HostRepoPathRequirement(envType))
	}
	if !info.IsDir() {
		return fmt.Errorf("repository path %q is not a directory on this machine: %s", trimmed, HostRepoPathRequirement(envType))
	}
	return nil
}
