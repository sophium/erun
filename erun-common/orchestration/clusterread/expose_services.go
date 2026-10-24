package clusterread

import (
	"strings"
)

// IsForbiddenKubectlError keeps the RBAC case distinguishable from a real
// failure. It matches on the message because kubectl reports authorization
// through its exit status and text, not a typed error.
func IsForbiddenKubectlError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "forbidden")
}
