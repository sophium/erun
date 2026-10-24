package deploy

import (
	"strings"
)

// ParseDockerImageReference rejects a version string posing as a registry,
// which helm templates produce from .Chart.AppVersion.
func ParseDockerImageReference(image string) (registry, imageName, version string, ok bool) {
	if image == "" {
		return "", "", "", false
	}

	nameTag := image
	if idx := strings.LastIndex(image, "/"); idx >= 0 {
		registry = image[:idx]
		nameTag = image[idx+1:]
	}

	imageName, version, found := strings.Cut(nameTag, ":")
	if !found || strings.TrimSpace(imageName) == "" || strings.TrimSpace(version) == "" {
		return "", "", "", false
	}

	if dockerRegistryLooksLikeVersion(registry) {
		return "", "", "", false
	}

	return registry, imageName, version, true
}
