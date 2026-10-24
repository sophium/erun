package build

import (
	"strings"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

// incrementalCacheDecision mirrors traceIncrementalDecision so the timing cache tag never disagrees.
// applicable is false when the build has no fingerprint, which is not a miss.
func incrementalCacheDecision(buildInput shared.DockerBuildSpec) (hit, applicable bool, missReason string) {
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

func dockerBuildStepName(buildInput shared.DockerBuildSpec) string {
	if name := strings.TrimSpace(buildInput.Image.ImageName); name != "" {
		return name
	}
	return strings.TrimSpace(buildInput.Image.Tag)
}
