package config

import (
	"os"
	"strings"
)

// IsInRuntimeEnvironment reports whether the process runs in an erun pod. Every
// chart sets a non-host ERUN_ENV_TYPE; ERUN_REPO_REMOTE is a fallback for older charts.
func IsInRuntimeEnvironment(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	if envType := EnvironmentType(strings.TrimSpace(env("ERUN_ENV_TYPE"))); envType.IsValid() {
		return envType != EnvironmentTypeHost
	}
	return strings.EqualFold(strings.TrimSpace(env("ERUN_REPO_REMOTE")), "true")
}
