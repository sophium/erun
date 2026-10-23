package config

import (
	"strings"
)

const DefaultRuntimeImageName = "erun-devops"

const (
	DefaultDockerHubRegistryBaseURL = "https://hub.docker.com"
	DefaultGHCRRegistryBaseURL      = "https://ghcr.io"
)

// Resolved returns a fully-specified config, applying registry defaults to any unset field.
func (c RuntimeRegistryConfig) Resolved() RuntimeRegistryConfig {
	resolved := RuntimeRegistryConfig{
		Namespace:  strings.TrimSpace(c.Namespace),
		Repository: strings.TrimSpace(c.Repository),
		BaseURL:    strings.TrimSpace(c.BaseURL),
		TokenURL:   strings.TrimSpace(c.TokenURL),
		Insecure:   c.Insecure,
	}
	if resolved.Namespace == "" {
		resolved.Namespace = DefaultContainerRegistry
	}
	if resolved.Repository == "" {
		resolved.Repository = DefaultRuntimeImageName
	}
	if resolved.BaseURL == "" {
		switch host, _, isRegistryHost := OciRegistryHostFromNamespace(resolved.Namespace); {
		case namespaceIsGHCR(resolved.Namespace):
			resolved.BaseURL = DefaultGHCRRegistryBaseURL
		case isRegistryHost:
			// A namespace that names its own registry host is served by that host,
			// not by Docker Hub; defaulting to the Hub sent the listing to a
			// repository named after the host and answered 404. That host may speak
			// plain HTTP only (an insecure in-cluster registry), so honor Insecure
			// rather than always assuming TLS.
			scheme := "https://"
			if resolved.Insecure {
				scheme = "http://"
			}
			resolved.BaseURL = scheme + host
		default:
			resolved.BaseURL = DefaultDockerHubRegistryBaseURL
		}
	}
	if resolved.TokenURL == "" {
		resolved.TokenURL = DefaultGHCRRegistryBaseURL
	}
	return resolved
}

func namespaceIsGHCR(namespace string) bool {
	_, ok := GhcrOwnerFromNamespace(namespace)
	return ok
}

func GhcrOwnerFromNamespace(namespace string) (string, bool) {
	trimmed := strings.TrimSpace(namespace)
	if trimmed == "" {
		return "", false
	}
	const prefix = "ghcr.io"
	if !strings.HasPrefix(trimmed, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(trimmed, prefix)
	rest = strings.TrimPrefix(rest, "/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", false
	}
	return rest, true
}
