package core

import (
	"strings"
)

// InjectedRuntimePodIdentity returns the tenant/environment the runtime chart
// injected into this process. The chart sets both ERUN_TENANT and
// ERUN_ENVIRONMENT on every runtime container and nothing else does, so the pair
// is the authoritative "I am inside an erun runtime pod, serving this env"
// marker — a kubeconfig or an in-cluster context is not (both exist off-pod too).
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
