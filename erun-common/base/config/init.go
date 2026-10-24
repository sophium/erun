package config

import (
	"fmt"
	"slices"
	"strings"
)

const (
	DefaultEnvironment       = "local"
	DefaultContainerRegistry = "ghcr.io/sophium"
	InitializeCurrentProject = "Initialize current project"
)

func NormalizeNamespaceName(value string) string {
	var builder strings.Builder
	lastHyphen := false

	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastHyphen = false
		case !lastHyphen && builder.Len() > 0:
			builder.WriteByte('-')
			lastHyphen = true
		}
	}

	result := strings.Trim(builder.String(), "-")
	if len(result) > 63 {
		result = strings.Trim(result[:63], "-")
	}
	return result
}

// maxTenantNameLength caps a tenant name at the RFC 1123 DNS label limit. The
// combined `<tenant>-<env>` namespace is normalized and truncated separately by
// normalizeNamespaceName; this bound just keeps a tenant name a single label.
const maxTenantNameLength = 63

// ValidateTenantName enforces that a tenant name is a single DNS-safe label of
// lowercase letters and digits with no hyphens. Tenant names feed the
// `<tenant>-<env>` namespace mapping (KubernetesNamespaceName); forbidding
// hyphens keeps that mapping unambiguous (split on the first hyphen) and
// injective, so `a-b`+`c` and `a`+`b-c` can no longer collide on one namespace.
//
// The rule is checked before normalization so a caller cannot rely on
// normalizeNamespaceName to silently repair an invalid name. It is enforced at
// tenant creation only; tenants created before this rule keep working unchanged
// (no re-validation on load), preserving back-compat.
func ValidateTenantName(name string) error {
	if name == "" {
		return fmt.Errorf("tenant name is required")
	}
	if len(name) > maxTenantNameLength {
		return fmt.Errorf("invalid tenant name %q: must be at most %d characters", name, maxTenantNameLength)
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return fmt.Errorf("invalid tenant name %q: tenant names must use only lowercase letters and digits with no hyphens, so the <tenant>-<env> namespace is unambiguous", name)
	}
	return nil
}

func KubernetesNamespaceName(tenant, envName string) string {
	return NormalizeNamespaceName(tenant + "-" + envName)
}

// NormalizeImagePullSecrets trims and de-duplicates the requested secret names,
// keeping the order the operator gave so the chart renders them predictably.
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
