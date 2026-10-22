package eruncommon

import (
	"fmt"
	"strings"
)

// build_failure_classifier.go holds erun's own known infrastructure failure
// signatures -- a registry or the network giving up, never a verdict about the
// change under test. A failed build whose failure detail names one is not
// recorded against a review: `builds.successful` is a plain boolean with no
// non-verdict, so recording it would move the review to FAILED for a
// network or registry blip.

// buildInconclusiveSignatures are BuildKit's own clauses for a base image
// pull that never even resolved, e.g. "failed to solve: failed to resolve
// source metadata for ghcr.io/sophium/erun-devops:1.0.246: failed to
// authorize: failed to fetch oauth token: Post \"https://ghcr.io/token\":
// net/http: TLS handshake timeout" -- any one clause below is sufficient on
// its own, since each already names a transport or registry failure rather
// than anything about the code under test.
var buildInconclusiveSignatures = []string{
	"failed to resolve source metadata",
	"failed to fetch oauth token",
	"tls handshake timeout",
}

// BuildInconclusiveSignatures returns a copy of erun's own known
// infrastructure-failure signatures -- the exact strings
// classifyBuildFailureText matches against. It exists so a doc-drift check
// outside this package can cross-check the published spec of this classifier
// against the real list. Returns a copy so a caller cannot mutate the
// classifier's own matching behavior.
func BuildInconclusiveSignatures() []string {
	signatures := make([]string, len(buildInconclusiveSignatures))
	copy(signatures, buildInconclusiveSignatures)
	return signatures
}

// classifyBuildFailureText reports which known infrastructure signature (if
// any) appears in text, matched case-insensitively.
func classifyBuildFailureText(text string) (signature string, matched bool) {
	lower := strings.ToLower(text)
	for _, marker := range buildInconclusiveSignatures {
		if strings.Contains(lower, marker) {
			return marker, true
		}
	}
	return "", false
}

// ensureNotKnownInfrastructureBuildFailure refuses to record a failed build
// whose failure detail names a known infrastructure signature. The review
// stays where it is, and the remedy is to re-run the build once the signature
// clears rather than to record a FAILED verdict on the change.
func ensureNotKnownInfrastructureBuildFailure(successful bool, failureDetail string) error {
	if successful {
		return nil
	}
	signature, matched := classifyBuildFailureText(failureDetail)
	if !matched {
		return nil
	}
	return fmt.Errorf(
		"refusing to record a failed build: --failure-detail %q matches a known erun infrastructure "+
			"failure signature (%q) -- this is a statement about the network or registry, not the change; "+
			"re-run the build once the signature clears and record that result instead",
		failureDetail, signature)
}
