package config

import (
	"strings"
)

// ImageReleaseLine returns "erun" for erun-devops, otherwise the name without "-devops".
func ImageReleaseLine(name string) string {
	if name == DefaultRuntimeImageName {
		return "erun"
	}
	return strings.TrimSuffix(name, "-devops")
}

// RuntimeImageComponentName extracts the bare component name from a bare,
// registry-qualified, or tagged image reference.
func RuntimeImageComponentName(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	if idx := strings.LastIndex(image, "/"); idx >= 0 {
		image = image[idx+1:]
	}
	if idx := strings.Index(image, "@"); idx >= 0 {
		image = image[:idx]
	}
	if idx := strings.LastIndex(image, ":"); idx >= 0 {
		image = image[:idx]
	}
	return strings.TrimSpace(image)
}

// RuntimeImageReleaseLine classifies an image by release line; ok is false when
// it names no component, and callers must not guess.
func RuntimeImageReleaseLine(image string) (line string, ok bool) {
	name := RuntimeImageComponentName(image)
	if name == "" {
		return "", false
	}
	return ImageReleaseLine(name), true
}
