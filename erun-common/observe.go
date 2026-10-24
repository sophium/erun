package eruncommon

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

// ObserveGetArgs builds a read-only `kubectl get <resource> [name] -o json`
// invocation. name is optional: omitted, it lists every object of that kind in
// the namespace.
func ObserveGetArgs(req config.ShellLaunchParams, resource string, name ...string) []string {
	args := config.KubectlTargetArgs(req)
	args = append(args, "get", resource)
	args = append(args, name...)
	return append(args, "-o", "json")
}

// isKubectlUnknownResource reports whether stderr is kubectl's "this API
// isn't installed" shape, so a cluster with no cert-manager CRDs reports zero
// certificates instead of failing the whole observation.
func isKubectlUnknownResource(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "the server doesn't have a resource type") ||
		strings.Contains(lower, "no matches for kind") ||
		strings.Contains(lower, "could not find the requested resource")
}

func IsKubectlNotFound(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "notfound") ||
		strings.Contains(strings.ToLower(stderr), "not found")
}
