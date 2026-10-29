package diagnose

import (
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// A failed `docker build` returns only "exit status 1", so failures carry a reason read
// from BuildKit output for the durable timing record.

// buildkitStepErrorPattern matches BuildKit's per-step failure line, e.g.
//
//	#6 ERROR: process "/bin/sh -c curl ..." did not complete successfully: exit code: 28
var buildkitStepErrorPattern = regexp.MustCompile(`(?m)^#(\d+) ERROR: (.*)$`)

const (
	buildFailureReasonMaxLines  = 3
	buildFailureReasonMaxLength = 500
)

// DockerBuildFailureReason summarises a failed build's output, or "" when none is recognisable.
func DockerBuildFailureReason(output string) string {
	step, headline, ok := lastBuildkitStepError(output)
	if !ok {
		return truncateReason(lastMeaningfulErrorLine(output))
	}
	detail := strings.Join(buildkitStepOutputTail(output, step), "; ")
	if detail == "" {
		return truncateReason(headline)
	}
	// The step's own last words are the reason; the headline is only how BuildKit reported it.
	return truncateReason(detail + " (" + headline + ")")
}

func lastBuildkitStepError(output string) (step, headline string, ok bool) {
	matches := buildkitStepErrorPattern.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return "", "", false
	}
	last := matches[len(matches)-1]
	return last[1], strings.TrimSpace(last[2]), true
}

func buildkitStepOutputTail(output, step string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		match := core.BuildkitStepOutputPattern.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if match == nil || match[1] != step {
			continue
		}
		text := strings.TrimSpace(match[3])
		if text == "" {
			continue
		}
		lines = append(lines, text)
	}
	if len(lines) > buildFailureReasonMaxLines {
		lines = lines[len(lines)-buildFailureReasonMaxLines:]
	}
	return lines
}

// lastMeaningfulErrorLine covers output BuildKit never framed per step.
func lastMeaningfulErrorLine(output string) string {
	var found string
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "ERROR:") || strings.HasPrefix(trimmed, "error:") {
			found = trimmed
		}
	}
	return found
}

// JoinFailureReason keeps the step's words: only they identify which step stopped.
func JoinFailureReason(reason, diagnosis string) string {
	if reason == "" {
		return diagnosis
	}
	if diagnosis == "" {
		return reason
	}
	return reason + " — " + diagnosis
}

func truncateReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) <= buildFailureReasonMaxLength {
		return reason
	}
	return strings.TrimSpace(reason[:buildFailureReasonMaxLength]) + "…"
}

// DockerBuildStepError carries the reason read from a failed build's output; the process
// error stays reachable via errors.Unwrap.
type DockerBuildStepError struct {
	Reason string
	Err    error
}

func (e DockerBuildStepError) Error() string {
	if e.Reason == "" {
		return e.Err.Error()
	}
	return e.Reason
}

func (e DockerBuildStepError) Unwrap() error {
	return e.Err
}
