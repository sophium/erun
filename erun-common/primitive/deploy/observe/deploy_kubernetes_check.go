package observe

import (
	"regexp"
	"strings"
)

// KubernetesAPIServerUnreachableSignal is shared with the doctor deploy
// diagnosis so both classify "cluster unreachable" the same way.
func KubernetesAPIServerUnreachableSignal(output string) bool {
	message := strings.ToLower(output)
	return strings.Contains(message, "unable to connect to the server") ||
		strings.Contains(message, "connection refused") ||
		strings.Contains(message, "no such host") ||
		strings.Contains(message, "i/o timeout") ||
		strings.Contains(message, "no configuration has been provided")
}

// klogFramePattern matches klog's severity/timestamp/goroutine/source prefix.
var klogFramePattern = regexp.MustCompile(`^[EWIF]\d{4} \d{2}:\d{2}:\d{2}\.\d+\s+\d+ \S+\.go:\d+\]\s*`)

// Greedy so an escaped \" inside the nested message does not end the match.
var klogUnhandledErrorPattern = regexp.MustCompile(`^"Unhandled Error"\s+err="(.*)"$`)

// SanitizeKubectlFailureOutput reduces kubectl output to its last actionable
// line: a fixed-width toast would otherwise cut off the cause.
func SanitizeKubectlFailureOutput(raw string) string {
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(klogFramePattern.ReplaceAllString(lines[i], ""))
		if line == "" {
			continue
		}
		if match := klogUnhandledErrorPattern.FindStringSubmatch(line); match != nil {
			return strings.ReplaceAll(match[1], `\"`, `"`)
		}
		return line
	}
	return ""
}
