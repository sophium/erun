package clusterread

import (
	"strings"
)

// IsForbiddenKubectlError matches on text: kubectl reports authorization only by exit status and message.
func IsForbiddenKubectlError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "forbidden")
}
