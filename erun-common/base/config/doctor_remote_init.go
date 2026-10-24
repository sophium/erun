package config

import (
	"os"
	"strings"
)

// IsInRuntimeEnvironment reports whether the current process is running
// inside an erun runtime pod. The chart sets ERUN_ENV_TYPE on every pod it
// renders to local-agent, remote-agent, or runtime — never to host, the one
// EnvironmentType with no pod — so a valid, non-host value is true for any
// pod regardless of how its worktree is stored. ERUN_REPO_REMOTE records
// worktree storage, not pod-ness, and stays false for a local-agent pod
// (hostPath-mounted worktree) even though it runs in a pod; it is kept only
// as a fallback for a pod deployed by a chart that predates ERUN_ENV_TYPE.
func IsInRuntimeEnvironment(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	if envType := EnvironmentType(strings.TrimSpace(env("ERUN_ENV_TYPE"))); envType.IsValid() {
		return envType != EnvironmentTypeHost
	}
	return strings.EqualFold(strings.TrimSpace(env("ERUN_REPO_REMOTE")), "true")
}
