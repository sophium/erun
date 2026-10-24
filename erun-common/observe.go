package eruncommon

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
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

// RunObserveKubectl runs a read-only kubectl get, returning stderr alongside
// any error so callers can distinguish "not found" and "unknown resource
// type" from a real failure without re-parsing a wrapped error string.
func RunObserveKubectl(args []string) (stdout []byte, stderr string, err error) {
	cmd := core.Command("kubectl", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	out, runErr := cmd.Output()
	return out, strings.TrimSpace(errBuf.String()), runErr
}

// KubectlErrorMessage folds stderr into err, sanitizing kubectl's raw output
// through sanitizeKubectlFailureOutput first: an unreachable API server makes
// kubectl retry and emit klog's "Unhandled Error" frame once per retry, and
// without this every goroutine id and source location reached the operator
// verbatim, repeated (erun#2392).
func KubectlErrorMessage(err error, stderr string) error {
	if stderr == "" {
		return err
	}
	if sanitized := sanitizeKubectlFailureOutput(stderr); sanitized != "" {
		return fmt.Errorf("%w: %s", err, sanitized)
	}
	return fmt.Errorf("%w: %s", err, stderr)
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
