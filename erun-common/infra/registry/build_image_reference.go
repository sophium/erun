package registry

import (
	"strings"
)

func DockerNamespaceFromTag(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	if cut := strings.IndexByte(tag, ':'); cut >= 0 {
		if last := strings.LastIndexByte(tag, '/'); last < 0 || cut > last {
			tag = tag[:cut]
		}
	}
	parts := strings.Split(tag, "/")
	if len(parts) < 2 {
		return ""
	}
	first := parts[0]
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		if len(parts) >= 3 {
			return parts[1]
		}
		return ""
	}
	return parts[0]
}

func DockerRegistryFromImageTag(tag string) string {
	first, _, ok := strings.Cut(tag, "/")
	if !ok {
		return ""
	}
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return first
	}
	return ""
}
