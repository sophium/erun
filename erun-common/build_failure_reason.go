package eruncommon

import (
	"regexp"
	"strings"
)

// A failed `docker build` returns nothing but "exit status 1". That is enough
// when a human is watching the stream it also printed, and useless in the case
// erun actually needs it for: a gate running detached in a merge queue, whose
// durable timing record is read long after the stream is gone. Every failure
// therefore carries a reason extracted from BuildKit's own output, so
// ~/.erun/timing/build-*.json says what went wrong rather than that something
// did.

// buildkitStepErrorPattern matches BuildKit's per-step failure line, e.g.
//
//	#6 ERROR: process "/bin/sh -c curl ..." did not complete successfully: exit code: 28
//
// The step number is what ties the failure back to the output lines that
// explain it.
var buildkitStepErrorPattern = regexp.MustCompile(`(?m)^#(\d+) ERROR: (.*)$`)

// buildkitStepOutputPattern matches one line a step printed, e.g.
//
//	#6 31.56 curl: (28) Connection timed out after 30002 milliseconds
//
// The elapsed-time field is what distinguishes a step's own output from
// BuildKit's framing around it.
var buildkitStepOutputPattern = regexp.MustCompile(`^#(\d+) (\d+\.\d+) (.*)$`)

const (
	// buildFailureReasonMaxLines bounds how much of the failing step's tail is
	// kept. The explanation is essentially always the last thing it printed;
	// more than a few lines turns a record meant to be skimmed into a log.
	buildFailureReasonMaxLines = 3
	// buildFailureReasonMaxLength keeps one record's error field readable when
	// a step's last line is a wall of text.
	buildFailureReasonMaxLength = 500
)

// dockerBuildFailureReason summarises why a `docker build` failed, from the
// captured BuildKit output. It returns "" when the output carries no
// recognisable failure, in which case the caller keeps the plain process error
// rather than inventing a reason for it.
func dockerBuildFailureReason(output string) string {
	step, headline, ok := lastBuildkitStepError(output)
	if !ok {
		return truncateReason(lastMeaningfulErrorLine(output))
	}
	detail := strings.Join(buildkitStepOutputTail(output, step), "; ")
	if detail == "" {
		return truncateReason(headline)
	}
	// The step's own last words first: "curl: (28) Connection timed out" is the
	// reason, and "did not complete successfully: exit code: 28" is only how
	// BuildKit reported it.
	return truncateReason(detail + " (" + headline + ")")
}

// lastBuildkitStepError returns the step number and headline of the last
// per-step failure BuildKit reported, which for a sequential build is the one
// that stopped it.
func lastBuildkitStepError(output string) (step, headline string, ok bool) {
	matches := buildkitStepErrorPattern.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return "", "", false
	}
	last := matches[len(matches)-1]
	return last[1], strings.TrimSpace(last[2]), true
}

// buildkitStepOutputTail returns the last few lines the named step printed
// before it failed, oldest first.
func buildkitStepOutputTail(output, step string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		match := buildkitStepOutputPattern.FindStringSubmatch(strings.TrimRight(line, "\r"))
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

// lastMeaningfulErrorLine is the fallback for output BuildKit never got far
// enough to frame -- a daemon that refused the build, an unresolvable base
// image -- where the last ERROR line is all there is to go on.
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

// joinFailureReason puts a diagnosis after the step's own words rather than in
// place of them: the diagnosis explains the cause, but only the step's output
// identifies which step and which download actually stopped.
func joinFailureReason(reason, diagnosis string) string {
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

// DockerBuildStepError wraps a failed `docker build` with the reason read out
// of its own output. Its Error() is that reason, so the message a caller prints
// and the message the timing record persists both say what failed instead of
// "exit status 1". The underlying process error stays reachable through
// errors.Unwrap for anything matching on exit codes.
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
