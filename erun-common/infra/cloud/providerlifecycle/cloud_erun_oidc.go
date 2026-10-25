package providerlifecycle

import (
	"strings"

	"github.com/sophium/erun/erun-common/infra/cloud/clouddefaults"
)

// erunLoginScope appends operator scopes, deduplicated; reserved scopes are often
// missing from scopes_supported, so they must be requestable by name.
func erunLoginScope(extra []string) string {
	scopes := strings.Fields(clouddefaults.ErunOAuthScope)
	seen := map[string]bool{}
	for _, s := range scopes {
		seen[s] = true
	}
	for _, candidate := range extra {
		for _, s := range strings.Fields(candidate) {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			scopes = append(scopes, s)
		}
	}
	return strings.Join(scopes, " ")
}
