package eruncommon

import (
	"fmt"
	"os"
	"strings"
)

// gate_run_failure_classifier.go is the shared classifier
// RunGateRunStart/RunGateRunReport apply before sending a caller-reported
// FAILED status to the platform: erun's own known infrastructure failure
// signatures -- a registry or the network giving up, never a verdict about
// the change under test -- are upgraded to INCONCLUSIVE automatically,
// instead of relying on whoever is driving the gate to recognize one by eye
// every time. See erun-backend/erun-backend-api/AGENTS.md "Gate Runs" and
// its "hand off to erun vs keep as operator policy" decision: a real
// merge-queue session reported the identical ghcr.io TLS-handshake-timeout
// failure as a red verdict three times before this existed.

// gateRunInconclusiveSignatures are BuildKit's own clauses for a base image
// pull that never even resolved, e.g. "failed to solve: failed to resolve
// source metadata for ghcr.io/sophium/erun-devops:1.0.246: failed to
// authorize: failed to fetch oauth token: Post \"https://ghcr.io/token\":
// net/http: TLS handshake timeout" -- any one clause below is sufficient on
// its own, since each already names a transport or registry failure rather
// than anything about the code under test.
var gateRunInconclusiveSignatures = []string{
	"failed to resolve source metadata",
	"failed to fetch oauth token",
	"tls handshake timeout",
}

// GateRunInconclusiveSignatures returns a copy of erun's own known
// infrastructure-failure signatures -- the exact strings
// classifyGateRunFailureText matches against. It exists so a doc-drift check
// outside this package (erun-integration's structural gates) can cross-check
// erun-docs/docs/agent-reference/skills-spec.md's spec of this classifier
// against the real list, instead of a copy that can silently drift from it.
// Returns a copy, not the package slice itself, so a caller cannot mutate the
// classifier's own matching behavior.
func GateRunInconclusiveSignatures() []string {
	signatures := make([]string, len(gateRunInconclusiveSignatures))
	copy(signatures, gateRunInconclusiveSignatures)
	return signatures
}

// classifyGateRunFailureText reports which known infrastructure signature
// (if any) appears in text, matched case-insensitively.
func classifyGateRunFailureText(text string) (signature string, matched bool) {
	lower := strings.ToLower(text)
	for _, marker := range gateRunInconclusiveSignatures {
		if strings.Contains(lower, marker) {
			return marker, true
		}
	}
	return "", false
}

// ensureNotKnownInfrastructureGateBuildFailure is RunReviewRecordBuild's
// counterpart to reclassifyKnownInfrastructureGateFailure: `builds.successful`
// is a plain boolean with no INCONCLUSIVE (see AGENTS.md "Gate Runs" on why
// gate_runs is a separate table), so a failed GATE build for a known
// infrastructure signature cannot be silently reclassified the way a gate
// run's own status can -- recording it FAILED would still move the review
// out of the merge queue for a network/registry blip, gaining nothing from
// the gate-run classifier a caller already gets right. Refuse instead, the
// same fail-closed shape checkDesktopPlaywrightCoverageForGate already uses
// in this file's sibling preflight, so the caller reports the gate run
// INCONCLUSIVE and re-drives the review once the signature clears rather
// than recording a false FAILED build.
func ensureNotKnownInfrastructureGateBuildFailure(gate, successful bool, failureDetail string) error {
	if !gate || successful {
		return nil
	}
	signature, matched := classifyGateRunFailureText(failureDetail)
	if !matched {
		return nil
	}
	return fmt.Errorf(
		"refusing to record a failed GATE build: --failure-detail %q matches a known erun infrastructure "+
			"failure signature (%q) -- this is a statement about the network or registry, not the change; "+
			"report the gate run INCONCLUSIVE instead (erun exec gate-run report <gateRunId> --status "+
			"inconclusive --failing-step ... --log-ref ...) and re-drive this review once the signature "+
			"clears, rather than recording a FAILED build for it",
		failureDetail, signature)
}

// reclassifyKnownInfrastructureGateFailure upgrades a caller-reported FAILED
// status to INCONCLUSIVE when failingStep or logRef names one of erun's own
// known infrastructure failure signatures, and leaves every other status
// untouched. Traced unconditionally -- not only under --dry-run -- so the
// override is visible even to a caller that only checks the reported
// status, never silently.
func reclassifyKnownInfrastructureGateFailure(ctx Context, status, failingStep, logRef string) string {
	if !strings.EqualFold(strings.TrimSpace(status), "failed") {
		return status
	}
	text := failingStep + " " + logRef + " " + logRefFileContentForClassification(logRef)
	signature, matched := classifyGateRunFailureText(text)
	if !matched {
		return status
	}
	ctx.Trace(fmt.Sprintf(
		"gate-run: %q matches a known infrastructure failure signature -- reporting inconclusive instead of failed",
		signature))
	return "inconclusive"
}

// logRefFileContentForClassification reads logRef's content when it
// resolves to an existing, reasonably small regular file, and returns ""
// otherwise. --log-ref is documented as "a job id, URL, or path" -- most
// values given to it are not a local file at all, e.g. the merge-queue-drive
// skill's rung 4 points it at a saved `erun build --output json` capture --
// so a value that is not a readable file must stay silent rather than
// erroring; it just contributes nothing to the classification text.
// maxLogRefFileBytesForClassification bounds how much of a log-ref file this
// reads into memory for classification -- generous enough for a captured
// `erun build` failure, small enough that a caller pointing --log-ref at
// something enormous (or not a log at all) cannot make this expensive.
const maxLogRefFileBytesForClassification = 1 << 20 // 1 MiB

func logRefFileContentForClassification(logRef string) string {
	path := strings.TrimSpace(logRef)
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxLogRefFileBytesForClassification {
		return ""
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(content)
}
