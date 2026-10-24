package clusterread

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

// ObserveGetArgs builds `kubectl get <resource> [name] -o json`; without name it lists the kind.
func ObserveGetArgs(req config.ShellLaunchParams, resource string, name ...string) []string {
	args := config.KubectlTargetArgs(req)
	args = append(args, "get", resource)
	args = append(args, name...)
	return append(args, "-o", "json")
}

// isKubectlUnknownResource lets a cluster without cert-manager CRDs report zero certificates.
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
