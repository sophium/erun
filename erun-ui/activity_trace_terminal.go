package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/primitive/open/appsession"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

// feedActivityTraceFromTerminal feeds each complete PTY line to the trace handler.
func (a *App) feedActivityTraceFromTerminal(managed *terminal.Session, chunk []byte) {
	if managed == nil || a.activityQueue == nil || !terminalFeedsActivityTrace(managed) {
		return
	}
	var lines []string
	a.terminal.WithLock(func(*terminal.Locked) {
		managed.ActivityTraceBuffer += stripActivityTraceANSI(string(chunk))
		lines = drainActivityTraceLines(managed)
	})
	if len(lines) == 0 {
		return
	}
	handler := newActivityTraceLineHandler(a, managed.Selection, managed.Kind)
	for _, line := range lines {
		handler(line)
		signalSessionReadyOnLine(managed, line)
		// Another window re-attached this session, so its PTY exit must not reconnect.
		if strings.TrimSpace(line) == appsession.ShellSessionTakenOverNotice {
			a.terminal.MarkTakenOver(managed)
		}
	}
}

// terminalFeedsActivityTrace excludes orchestrator panes.
func terminalFeedsActivityTrace(managed *terminal.Session) bool {
	if managed.Selection.Tenant == "" || managed.Selection.Environment == "" {
		return false
	}
	switch managed.Kind {
	case terminal.KindLocal, terminal.KindOpen, terminal.KindAI, terminal.KindCommand,
		terminal.KindContributeERun, terminal.KindContributeAI:
		return true
	case terminal.KindOrchestrator:
		fallthrough
	default:
		return false
	}
}

// drainActivityTraceLines keeps the trailing partial line buffered; caller holds the terminal lock.
func drainActivityTraceLines(managed *terminal.Session) []string {
	lines := []string{}
	for {
		idx := strings.IndexByte(managed.ActivityTraceBuffer, '\n')
		if idx < 0 {
			break
		}
		raw := managed.ActivityTraceBuffer[:idx]
		// Keep the text after the last `\r` (spinner overwrites) once the trailing
		// `\r` of `\r\n` is trimmed, matching what the user sees.
		line := strings.TrimRight(raw, "\r")
		if r := strings.LastIndexByte(line, '\r'); r >= 0 {
			line = line[r+1:]
		}
		lines = append(lines, line)
		managed.ActivityTraceBuffer = managed.ActivityTraceBuffer[idx+1:]
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

func signalSessionReadyOnLine(managed *terminal.Session, line string) {
	if managed == nil {
		return
	}
	trimmed := strings.TrimSpace(line)
	switch {
	case sessionReadyDeployedRe.MatchString(trimmed),
		sessionReadySkippedRe.MatchString(trimmed),
		sessionReadyAttachedRe.MatchString(trimmed),
		sessionReadyShellPromptRe.MatchString(trimmed):
		managed.SignalReady(nil)
	case sessionReadyFailedRe.MatchString(trimmed):
		managed.SignalReady(fmt.Errorf("%s", trimmed))
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
