package config

import (
	"strings"
)

// OciRegistryHostFromNamespace splits a namespace naming its own registry host
// into host and repository prefix; a Hub account (no dot or port) is not one.
func OciRegistryHostFromNamespace(namespace string) (string, string, bool) {
	trimmed := strings.Trim(strings.TrimSpace(namespace), "/")
	if trimmed == "" {
		return "", "", false
	}
	host, rest, _ := strings.Cut(trimmed, "/")
	if !strings.Contains(host, ".") && !strings.Contains(host, ":") {
		return "", "", false
	}
	return host, strings.Trim(rest, "/"), true
}
