package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/primitive/open"
)

// feedActivityTraceFromTerminal feeds each complete PTY line to the trace handler.
func (a *App) feedActivityTraceFromTerminal(managed *managedTerminal, chunk []byte) {
	if managed == nil || a.activityQueue == nil || !terminalFeedsActivityTrace(managed) {
		return
	}
	a.mu.Lock()
	managed.activityTraceBuffer += stripActivityTraceANSI(string(chunk))
	lines := drainActivityTraceLines(managed)
	a.mu.Unlock()
	if len(lines) == 0 {
		return
	}
	handler := newActivityTraceLineHandler(a, managed.selection, managed.kind)
	for _, line := range lines {
		handler(line)
		signalSessionReadyOnLine(managed, line)
		// Another window re-attached this session, so its PTY exit must not reconnect.
		if strings.TrimSpace(line) == open.ShellSessionTakenOverNotice {
			a.markSessionTakenOver(managed)
		}
	}
}

// terminalFeedsActivityTrace excludes orchestrator panes.
func terminalFeedsActivityTrace(managed *managedTerminal) bool {
	if managed.selection.Tenant == "" || managed.selection.Environment == "" {
		return false
	}
	switch managed.kind {
	case sessionKindLocal, sessionKindOpen, sessionKindAI, sessionKindCommand,
		sessionKindContributeERun, sessionKindContributeAI:
		return true
	case sessionKindOrchestrator:
		fallthrough
	default:
		return false
	}
}

// drainActivityTraceLines keeps the trailing partial line buffered; caller holds a.mu.
func drainActivityTraceLines(managed *managedTerminal) []string {
	lines := []string{}
	for {
		idx := strings.IndexByte(managed.activityTraceBuffer, '\n')
		if idx < 0 {
			break
		}
		raw := managed.activityTraceBuffer[:idx]
		// Keep the text after the last `\r` (spinner overwrites) once the trailing
		// `\r` of `\r\n` is trimmed, matching what the user sees.
		line := strings.TrimRight(raw, "\r")
		if r := strings.LastIndexByte(line, '\r'); r >= 0 {
			line = line[r+1:]
		}
		lines = append(lines, line)
		managed.activityTraceBuffer = managed.activityTraceBuffer[idx+1:]
	}
	return lines
}

// sessionReady*Re release the action runner's gate; any match means setup is done.
var (
	sessionReadyDeployedRe    = regexp.MustCompile(`^==> Deployed `)
	sessionReadyFailedRe      = regexp.MustCompile(`^==> Deploy (?:of \S+ )?failed`)
	sessionReadySkippedRe     = regexp.MustCompile(`^==> Skipping `)
	sessionReadyAttachedRe    = regexp.MustCompile(`^Defaulted container "[^"]+" out of:`)
	sessionReadyShellPromptRe = regexp.MustCompile(`[\w][\w.-]*@[\w][\w.-]*:[~/].*[\$#]\s*$`)
)

func signalSessionReadyOnLine(managed *managedTerminal, line string) {
	if managed == nil {
		return
	}
	trimmed := strings.TrimSpace(line)
	switch {
	case sessionReadyDeployedRe.MatchString(trimmed),
		sessionReadySkippedRe.MatchString(trimmed),
		sessionReadyAttachedRe.MatchString(trimmed),
		sessionReadyShellPromptRe.MatchString(trimmed):
		managed.signalReady(nil)
	case sessionReadyFailedRe.MatchString(trimmed):
		managed.signalReady(fmt.Errorf("%s", trimmed))
	}
}

// stripActivityTraceANSI strips only CSI sequences.
func stripActivityTraceANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == 0x1b && i+1 < len(runes) && runes[i+1] == '[' {
			j := i + 2
			for j < len(runes) {
				r := runes[j]
				if r >= '@' && r <= '~' {
					j++
					break
				}
				j++
			}
			i = j - 1
			continue
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}
