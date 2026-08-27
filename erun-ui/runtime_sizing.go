package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// runtime_sizing.go wires the Runtime tab's sizing recommendation and its
// "Resize to this" action to the same `erun resize` primitive the CLI and MCP
// transports use (erun-common/runtime_resize.go). The standing recommendation
// is derived from usage history retained inside the pod
// (erun-common/runtime_usage_history.go) and never leaves it, so both reads
// here run `erun resize` itself over kubectl exec rather than re-deriving the
// recommendation host-side from data the desktop cannot see.
//
// The preview and apply calls parse the command's own stable trace/output
// lines rather than `--output json`: kubectl exec's CombinedOutput merges the
// pod's stdout and stderr into one buffer, which would otherwise interleave
// the JSON payload with trace text unpredictably. Fixed-shape lines
// (`resize: ... -> ...`, `==> Resized ...`) are the same "public API" contract
// activity_queue_app.go already parses from piped CLI output (erun-ui/AGENTS.md
// § Command Completion And State-Refresh Wiring).

const runtimeSizingTimeout = 15 * time.Second

// runtimeSizingActionLine matches "resize: <tenant>/<env> <resource> <from> -> <to>".
var runtimeSizingActionLine = regexp.MustCompile(`^resize: \S+/\S+ (cpu|memory) (\S+) -> (\S+)$`)

// runtimeSizingNoOpLine matches "resize: <tenant>/<env> is already sized at cpu=X memory=Y; no change".
var runtimeSizingNoOpLine = regexp.MustCompile(`^resize: \S+/\S+ is already sized at `)

// uiRuntimeSizingAction is one resource's resolved change, mirroring
// eruncommon.RuntimeResizeAction.
type uiRuntimeSizingAction struct {
	Resource string `json:"resource"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// uiRuntimeSizingRecommendation is the Runtime tab's read model for "what does
// this environment think it should be sized as" plus the outcome of acting on
// it. Available=false with Message set covers both "nothing to recommend yet"
// and "could not read it" -- the tab renders both as the same informational
// state, matching RuntimeUsageField's fail-soft contract.
type uiRuntimeSizingRecommendation struct {
	Tenant      string                  `json:"tenant"`
	Environment string                  `json:"environment"`
	Available   bool                    `json:"available"`
	Message     string                  `json:"message,omitempty"`
	NoOp        bool                    `json:"noOp,omitempty"`
	Actions     []uiRuntimeSizingAction `json:"actions,omitempty"`
}

// LoadRuntimeSizing previews applying the environment's own standing
// recommendation, read-only: it runs the same resolution
// `erun resize --apply-recommendation --dry-run` performs, inside the pod,
// and never writes anything.
func (a *App) LoadRuntimeSizing(selection uiSelection) (uiRuntimeSizingRecommendation, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiRuntimeSizingRecommendation{}, fmt.Errorf("tenant and environment are required")
	}
	ctx, cancel := context.WithTimeout(a.backgroundContext(), runtimeSizingTimeout)
	defer cancel()
	output, err := a.execInRuntimePod(ctx, selection, resizeCommandScript(selection, true, false))
	if err != nil {
		return uiRuntimeSizingRecommendation{
			Tenant:      selection.Tenant,
			Environment: selection.Environment,
			Message:     "Cannot read this environment's sizing recommendation: " + runtimeProbeFailureMessage(ctx, runtimeSizingTimeout, err, friendlyRuntimeResizeError),
		}, nil
	}
	return runtimeSizingFromOutput(selection, output), nil
}

// ResizeRuntimeToRecommendation applies the environment's own standing
// recommendation for real -- the "apply the recommendation directly" action,
// so the operator never retypes the numbers the tab already shows.
// overrideLease must be an explicit, deliberate second call: the tab's first
// click never sets it, and only a lease-held refusal offers the follow-up
// that does.
func (a *App) ResizeRuntimeToRecommendation(selection uiSelection, overrideLease bool) (uiRuntimeSizingRecommendation, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiRuntimeSizingRecommendation{}, fmt.Errorf("tenant and environment are required")
	}
	ctx, cancel := context.WithTimeout(a.backgroundContext(), runtimeReclaimTimeout)
	defer cancel()
	output, err := a.execInRuntimePod(ctx, selection, resizeCommandScript(selection, true, overrideLease))
	if err != nil {
		return uiRuntimeSizingRecommendation{}, fmt.Errorf("%s", runtimeProbeFailureMessage(ctx, runtimeReclaimTimeout, err, friendlyRuntimeResizeError))
	}
	result := runtimeSizingFromOutput(selection, output)
	result.Available = true
	if result.NoOp {
		a.emitAppNotification("info", fmt.Sprintf("%s/%s is already sized as recommended.", selection.Tenant, selection.Environment))
	} else {
		a.emitAppNotification("info", fmt.Sprintf("Resized %s/%s to the standing recommendation.", selection.Tenant, selection.Environment))
	}
	return result, nil
}

// friendlyRuntimeResizeError strips the kubectl-exec wrapper text a failed
// call otherwise carries, so the tab surfaces the resize command's own
// message (e.g. the lease-held refusal, or "no standing recommendation is
// available") rather than a raw exit-status/kubectl error. It scans line by
// line for the last "resize: ..." or "resize refused: ..." line rather than
// searching the whole blob for a colon, since a real failure's combined
// output also carries every trace line printed before the error and those
// contain colons of their own.
func friendlyRuntimeResizeError(err error) string {
	text := err.Error()
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		for _, prefix := range []string{"resize refused: ", "resize: "} {
			if strings.HasPrefix(line, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(line, prefix))
			}
		}
	}
	return text
}

// resizeCommandScript builds the in-pod invocation. dryRun previews without
// writing; overrideLease is only meaningful (and only ever passed) on a real
// apply. Redirecting stderr into the captured stream is what lets the parser
// below see the command's trace lines, which under --dry-run only print at
// trace verbosity (root AGENTS.md: "--dry-run implies trace verbosity").
func resizeCommandScript(selection uiSelection, dryRun, overrideLease bool) string {
	args := []string{
		"erun", "resize",
		"--tenant", shellSingleQuote(selection.Tenant),
		"--environment", shellSingleQuote(selection.Environment),
		"--apply-recommendation",
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	if overrideLease {
		args = append(args, "--override-lease")
	}
	return strings.Join(args, " ") + " 2>&1"
}

// shellSingleQuote quotes a value for embedding in a POSIX shell command
// line. Tenant/environment names are already constrained elsewhere (they name
// real Kubernetes namespaces), but the script is assembled as a string handed
// to `/bin/sh -c`, so this is defensive rather than load-bearing.
func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// runtimeSizingFromOutput parses the resize command's own stable lines out of
// its combined output. Anything it doesn't recognise is dropped rather than
// surfaced -- a version drift here should read as "no actions shown", not
// garbled output, and the exit code (already checked by the caller) is what
// actually gates success/failure.
func runtimeSizingFromOutput(selection uiSelection, output string) uiRuntimeSizingRecommendation {
	result := uiRuntimeSizingRecommendation{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Available:   true,
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if runtimeSizingNoOpLine.MatchString(line) {
			result.NoOp = true
			continue
		}
		if match := runtimeSizingActionLine.FindStringSubmatch(line); match != nil {
			result.Actions = append(result.Actions, uiRuntimeSizingAction{Resource: match[1], From: match[2], To: match[3]})
		}
	}
	if !result.NoOp && len(result.Actions) == 0 {
		result.Available = false
		result.Message = "No standing sizing recommendation is available for this environment yet."
	}
	return result
}
