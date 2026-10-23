package config

import (
	"strings"
)

// OciRegistryHostFromNamespace splits a namespace that names a registry host of
// its own -- a private registry, an ECR account, an in-cluster registry -- into
// that host and any repository prefix below it. A Docker Hub namespace is a bare
// account name with neither a dot nor a port, so it stays on the Hub path.
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
