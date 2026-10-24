package build

import (
	"fmt"
	"strings"
)

// A build failing on a known infrastructure signature is not recorded against a review:
// `builds.successful` has no non-verdict, so it would mark the review FAILED.

// buildInconclusiveSignatures name registry, network or daemon failures, never the change.
// A bare "failed to create network" is excluded: a test's own network can fail for its own reasons.
var buildInconclusiveSignatures = []string{
	"failed to resolve source metadata",
	"failed to fetch oauth token",
	"tls handshake timeout",
	"esockettimedout",
	"wait for reaper",
	"reaper:",
}

// BuildInconclusiveSignatures returns a copy of the signatures, for doc-drift checks.
func BuildInconclusiveSignatures() []string {
	signatures := make([]string, len(buildInconclusiveSignatures))
	copy(signatures, buildInconclusiveSignatures)
	return signatures
}

func classifyBuildFailureText(text string) (signature string, matched bool) {
	lower := strings.ToLower(text)
	for _, marker := range buildInconclusiveSignatures {
		if strings.Contains(lower, marker) {
			return marker, true
		}
	}
	return "", false
}

// EnsureNotKnownInfrastructureBuildFailure refuses to record a failed build that names a
// known infrastructure signature; re-run it instead.
func EnsureNotKnownInfrastructureBuildFailure(successful bool, failureDetail string) error {
	if successful {
		return nil
	}
	signature, matched := classifyBuildFailureText(failureDetail)
	if !matched {
		return nil
	}
	return fmt.Errorf(
		"refusing to record a failed build: --failure-detail %q matches a known erun infrastructure "+
			"failure signature (%q) -- this is a statement about the network, registry, or Docker daemon, not the change; "+
			"re-run the build once the signature clears and record that result instead",
		failureDetail, signature)
}
