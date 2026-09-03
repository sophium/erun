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
