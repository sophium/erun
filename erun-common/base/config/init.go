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

// maxTenantNameLength is the RFC 1123 DNS label limit.
const maxTenantNameLength = 63

// ValidateTenantName requires one lowercase alphanumeric label with no hyphens,
// so the `<tenant>-<env>` namespace split stays injective. Enforced at creation only.
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
