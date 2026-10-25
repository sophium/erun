package containerregistry

import (
	"slices"
	"strings"
)

// NormalizeImagePullSecrets trims and de-duplicates names, preserving order.
func NormalizeImagePullSecrets(names []string) []string {
	normalized := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || slices.Contains(normalized, name) {
			continue
		}
		normalized = append(normalized, name)
	}
	return normalized
}
