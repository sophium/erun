package core

import (
	"strings"
)

// InjectedRuntimePodIdentity returns the tenant/environment the runtime chart
// injected. Only the chart sets both, so the pair marks a runtime pod.
func InjectedRuntimePodIdentity(env func(string) string) (tenant string, environment string, ok bool) {
	if env == nil {
		return "", "", false
	}
	tenant = strings.TrimSpace(env("ERUN_TENANT"))
	environment = strings.TrimSpace(env("ERUN_ENVIRONMENT"))
	if tenant == "" || environment == "" {
		return "", "", false
	}
	return tenant, environment, true
}
