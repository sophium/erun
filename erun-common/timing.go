package eruncommon

import (
	"strings"
)

// incrementalCacheDecision derives the same hit/reason a build's fingerprint
// decision already gives the trace (traceIncrementalDecision), so the timing
// report's cache tag never disagrees with it. applicable is false when the
// build has no fingerprint at all (incremental caching disabled or not
// computed for it), which is not a cache miss and should carry no tag.
func incrementalCacheDecision(buildInput DockerBuildSpec) (hit, applicable bool, missReason string) {
	if buildInput.Fingerprint == "" {
		return false, false, ""
	}
	if buildInput.Promote {
		return true, true, ""
	}
	switch {
	case strings.TrimSpace(buildInput.CascadeRebuildFromTag) != "":
		return false, true, "dependency " + strings.TrimSpace(buildInput.CascadeRebuildFromTag) + " is rebuilding"
	case len(buildInput.MissingFingerprintPlatforms) > 0:
		return false, true, "fingerprint image is missing for " + describeMissingPlatforms(buildInput.MissingFingerprintPlatforms)
	default:
		return false, true, "no cached fingerprint image"
	}
}

// dockerBuildStepName is the timing tree's label for one image build — the
// short image name when known, so a table row reads "erun-console" rather
// than the full registry tag.
func dockerBuildStepName(buildInput DockerBuildSpec) string {
	if name := strings.TrimSpace(buildInput.Image.ImageName); name != "" {
		return name
	}
	return strings.TrimSpace(buildInput.Image.Tag)
}
