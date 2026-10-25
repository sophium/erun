package activity

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/primitive/open/appsession"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// FeedTrace hands each complete PTY line of a session to the trace handler and
// releases the session's ready gate on a setup-done line. It reports whether a
// line said another window took the session over, so its PTY exit must not
// reconnect.
func (s *Service) FeedTrace(selection uimodel.Selection, kind string, lines []string, signalReady func(error)) bool {
	if len(lines) == 0 {
		return false
	}
	takenOver := false
	handler := s.TraceLineHandler(selection, kind)
	for _, line := range lines {
		handler(line)
		signalSessionReadyOnLine(signalReady, line)
		if strings.TrimSpace(line) == appsession.ShellSessionTakenOverNotice {
			takenOver = true
		}
	}
	return takenOver
}

// FeedsTrace excludes orchestrator panes and sessions with no environment.
func FeedsTrace(selection uimodel.Selection, kind string) bool {
	if selection.Tenant == "" || selection.Environment == "" {
		return false
	}
	switch kind {
	case KindLocal, KindOpen, KindAI, KindCommand, KindContributeERun, KindContributeAI:
		return true
	default:
		return false
	}
}

// DrainTraceLines splits buffer into its complete lines and the trailing partial
// line, which stays buffered.
func DrainTraceLines(buffer string) (string, []string) {
	lines := []string{}
	for {
		idx := strings.IndexByte(buffer, '\n')
		if idx < 0 {
			break
		}
		raw := buffer[:idx]
		// Keep the text after the last `\r` (spinner overwrites) once the trailing
		// `\r` of `\r\n` is trimmed, matching what the user sees.
		line := strings.TrimRight(raw, "\r")
		if r := strings.LastIndexByte(line, '\r'); r >= 0 {
			line = line[r+1:]
		}
		lines = append(lines, line)
		buffer = buffer[idx+1:]
	}
	return buffer, lines
}

// sessionReady*Re release the action runner's gate; any match means setup is done.
var (
	sessionReadyDeployedRe    = regexp.MustCompile(`^==> Deployed `)
	sessionReadyFailedRe      = regexp.MustCompile(`^==> Deploy (?:of \S+ )?failed`)
	sessionReadySkippedRe     = regexp.MustCompile(`^==> Skipping `)
	sessionReadyAttachedRe    = regexp.MustCompile(`^Defaulted container "[^"]+" out of:`)
	sessionReadyShellPromptRe = regexp.MustCompile(`[\w][\w.-]*@[\w][\w.-]*:[~/].*[\$#]\s*$`)
)

func signalSessionReadyOnLine(signalReady func(error), line string) {
	if signalReady == nil {
		return
	}
	trimmed := strings.TrimSpace(line)
	switch {
	case sessionReadyDeployedRe.MatchString(trimmed),
		sessionReadySkippedRe.MatchString(trimmed),
		sessionReadyAttachedRe.MatchString(trimmed),
		sessionReadyShellPromptRe.MatchString(trimmed):
		signalReady(nil)
	case sessionReadyFailedRe.MatchString(trimmed):
		signalReady(fmt.Errorf("%s", trimmed))
	}
}

// StripTraceANSI strips only CSI sequences.
func StripTraceANSI(s string) string {
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
