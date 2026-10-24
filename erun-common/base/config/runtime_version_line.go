package config

import (
	"strings"
)

// ImageReleaseLine names the release line a bare component name belongs to:
// "erun" for the stock erun-devops image, or the tenant/component name
// (stripping "-devops") for any other <name>-devops image.
func ImageReleaseLine(name string) string {
	if name == DefaultRuntimeImageName {
		return "erun"
	}
	return strings.TrimSuffix(name, "-devops")
}

// RuntimeImageComponentName extracts the bare component name from a runtime
// image reference in any shape RuntimeImage/RuntimeRunningImage may take: a
// bare name ("erun-devops"), a registry-qualified tagless pin
// ("ghcr.io/sophium/frs-devops", the self-maintaining form
// stripRuntimeImageTag/resolveRuntimeImageOverride round-trip), or a fully
// resolved tagged reference ("ghcr.io/sophium/frs-devops:1.0.86"). Empty
// when image is empty.
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

// RuntimeImageReleaseLine classifies a runtime image reference in any shape
// (see runtimeImageComponentName) by release line. ok is false when image is
// empty or names no component -- callers must treat that as undetermined,
// never guess a line from convention alone.
func RuntimeImageReleaseLine(image string) (line string, ok bool) {
	name := RuntimeImageComponentName(image)
	if name == "" {
		return "", false
	}
	return ImageReleaseLine(name), true
}
