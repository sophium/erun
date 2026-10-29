package dockerregistry

import (
	"strings"
)

// CutLast splits around the last sep, so a registry with a port or org path still parses.
func CutLast(s, sep string) (before, after string, found bool) {
	index := strings.LastIndex(s, sep)
	if index < 0 {
		return s, "", false
	}
	return s[:index], s[index+len(sep):], true
}
