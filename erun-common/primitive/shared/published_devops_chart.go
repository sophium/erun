package shared

import (
	"strings"
)

// CutLast splits around the LAST separator, so a coordinate whose registry
// carries the same separator (a port in localhost:5000, an org path in
// ghcr.io/sophium) still parses.
func CutLast(s, sep string) (before, after string, found bool) {
	index := strings.LastIndex(s, sep)
	if index < 0 {
		return s, "", false
	}
	return s[:index], s[index+len(sep):], true
}
