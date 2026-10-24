package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Both reads run `erun resize` in the pod, since the recommendation's usage
// history never leaves it. Output is parsed from fixed-shape trace lines, not
// --output json: kubectl exec merges stdout and stderr into one buffer.

const runtimeSizingTimeout = 15 * time.Second

var runtimeSizingActionLine = regexp.MustCompile(`^resize: \S+/\S+ (cpu|memory) (\S+) -> (\S+)$`)

var runtimeSizingNoOpLine = regexp.MustCompile(`^resize: \S+/\S+ is already sized at `)

// Verdict/evidence lines come from lifecycle's traceRuntimeSizingRecommendation.
var (
	runtimeSizingVerdictLine  = regexp.MustCompile(`^resize: \S+/\S+ sizing: (.+)$`)
	runtimeSizingEvidenceLine = regexp.MustCompile(`^resize: \S+/\S+ sizing-evidence: (.+)$`)
)

// uiRuntimeSizingAction mirrors lifecycle.RuntimeResizeAction.
type uiRuntimeSizingAction struct {
	Resource string `json:"resource"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// uiRuntimeSizingRecommendation: Available=false with Message covers both
// "nothing to recommend" and "could not read it".
type uiRuntimeSizingRecommendation struct {
	Tenant      string                  `json:"tenant"`
	Environment string                  `json:"environment"`
	Available   bool                    `json:"available"`
	Message     string                  `json:"message,omitempty"`
	NoOp        bool                    `json:"noOp,omitempty"`
	Actions     []uiRuntimeSizingAction `json:"actions,omitempty"`
	// Verdicts and Evidence are set even on NoOp so the tab can explain why.
	Verdicts []string `json:"verdicts,omitempty"`
	Evidence string   `json:"evidence,omitempty"`
}

// LoadRuntimeSizing previews the standing recommendation via `erun resize --dry-run`; it writes nothing.
func (a *App) LoadRuntimeSizing(selection uiSelection) (uiRuntimeSizingRecommendation, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("load runtime sizing", selection.Tenant, selection.Environment); err != nil {
		return uiRuntimeSizingRecommendation{}, err
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

// ResizeRuntimeToRecommendation applies the standing recommendation. overrideLease
// is only set by the follow-up to a lease-held refusal.
func (a *App) ResizeRuntimeToRecommendation(selection uiSelection, overrideLease bool) (uiRuntimeSizingRecommendation, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("resize runtime to recommendation", selection.Tenant, selection.Environment); err != nil {
		return uiRuntimeSizingRecommendation{}, err
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

// friendlyRuntimeResizeError takes the last "resize: " / "resize refused: " line,
// since earlier trace lines contain colons too.
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

// stderr is merged so the parser sees trace lines, which --dry-run prints.
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

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// Unrecognised lines are dropped; the caller's exit-code check gates success.
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
			continue
		}
		if match := runtimeSizingVerdictLine.FindStringSubmatch(line); match != nil {
			result.Verdicts = append(result.Verdicts, match[1])
			continue
		}
		if match := runtimeSizingEvidenceLine.FindStringSubmatch(line); match != nil {
			result.Evidence = match[1]
		}
	}
	if !result.NoOp && len(result.Actions) == 0 && len(result.Verdicts) == 0 {
		result.Available = false
		result.Message = "No standing sizing recommendation is available for this environment yet."
	}
	return result
}
