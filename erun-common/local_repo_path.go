package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HostRepoPathRequirement names why a type needs a host-machine directory,
// worded for what that type actually does with it: local-agent hostPath-mounts
// it into a pod, while a host env has no pod at all and simply is that
// directory.
//
// It is the shared wording for every refusal, so a caller that rejects a path
// reports the same reason the env-init path reports for the same value.
func HostRepoPathRequirement(envType EnvironmentType) string {
	if envType == EnvironmentTypeHost {
		return "it needs a host directory to use — run init from the project directory or pass --project-root"
	}
	return "it needs a host repo path to mount — run init from the project directory or pass --project-root"
}

// RequiresHostRepoPath reports whether envType records a path on this machine
// that has to exist: a local-agent env hostPath-mounts it into its pod as the
// worktree, and a host env has no pod and simply is that directory. Remote and
// runtime envs ride a PVC worktree instead, so the path they record names an
// in-pod directory and carries no requirement here.
func RequiresHostRepoPath(envType EnvironmentType) bool {
	return envType == EnvironmentTypeLocalAgent || envType == EnvironmentTypeHost
}

// ValidateEnvRepoPath applies the host repo path requirement exactly for the
// types that have one and accepts any value for the types that do not, so a
// caller holding an env of unknown type gets the same verdict init would.
func ValidateEnvRepoPath(envType EnvironmentType, path string) error {
	if !RequiresHostRepoPath(envType) {
		return nil
	}
	return ValidateHostRepoPath(envType, path)
}

// ValidateHostRepoPath is the single definition of what a host-machine
// repository path must be. A local-agent env hostPath-mounts this path into the
// agent pod as its worktree, and a host env IS this directory, so both require a
// real directory on this machine, named absolutely. A relative or missing path
// is not recorded: it would surface later as an empty worktree or a deploy
// failure attributed to the mount rather than to the edit that caused it.
//
// Every caller that records such a path — the env-init path and the desktop's
// Manage dialog — calls this, so the set of accepted paths is one set with one
// reason, not two that drift.
func ValidateHostRepoPath(envType EnvironmentType, path string) error {
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
