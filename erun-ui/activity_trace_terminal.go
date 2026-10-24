package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/primitive/open"
)

// feedActivityTraceFromTerminal drives the activity queue from any tab that
// hosts a running erun process (Local from the Deploy button, ERun from a
// manual `erun deploy`, AI from Claude deploying inside the pod), dispatching
// each complete line through the trace handler that owns deploy lifecycle.
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
		// newActivityTraceLineHandler itself buffers the line into entry.Detail
		// before dispatching, so every caller (this PTY reader and the
		// subprocess-captured orchestration path) gets the same Detail capture.
		handler(line)
		signalSessionReadyOnLine(managed, line)
		// The CLI's taken-over notice is a public contract line (see
		// eruncommon.ShellSessionTakenOverNotice): another ERun window
		// re-attached this persistent session, so the upcoming PTY exit
		// must not trigger a reconnect that would steal it back.
		if strings.TrimSpace(line) == open.ShellSessionTakenOverNotice {
			a.markSessionTakenOver(managed)
		}
	}
}

// terminalFeedsActivityTrace reports whether a tab is one that can run erun
// against a selected environment; an orchestrator pane is not.
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

// drainActivityTraceLines returns every complete `\n`-terminated line, leaving
// any trailing partial line buffered for the next chunk. The caller must hold
// a.mu.
func drainActivityTraceLines(managed *managedTerminal) []string {
	lines := []string{}
	for {
		idx := strings.IndexByte(managed.activityTraceBuffer, '\n')
		if idx < 0 {
			break
		}
		raw := managed.activityTraceBuffer[:idx]
		// Strip the trailing `\r` of the PTY's `\r\n`, then on a spinner-style
		// overwrite (`\rprogress\rprogress\rfinal`) keep only the content after
		// the LAST `\r` so the text matches what the user sees. Taking the first
		// `\r` instead would blank the common `text\r\n` case and break every
		// downstream matcher.
		line := strings.TrimRight(raw, "\r")
		if r := strings.LastIndexByte(line, '\r'); r >= 0 {
			line = line[r+1:]
		}
		lines = append(lines, line)
		managed.activityTraceBuffer = managed.activityTraceBuffer[idx+1:]
	}
	return lines
}

// sessionReady*Re match lines that indicate the session's setup phase
// is done. signalSessionReadyOnLine uses them to release the desktop
// action runner's gate so the next queued action can start. The
// matchers are intentionally broad — any of them firing means the user
// is past the setup phase and a parallel queued action can safely run.
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

// stripActivityTraceANSI removes the most common ANSI control sequences erun
// emits via spinner/colors so trace-line matching survives terminal noise.
// The patterns are intentionally narrow (CSI-style escape sequences) to
// avoid clobbering meaningful output.
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
