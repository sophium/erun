package workspace

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

// workspaceSyncLogWindow bounds silence: no-op passes fold into one aggregate line per
// window, so a quiet log means nothing changed rather than a dead loop.
const workspaceSyncLogWindow = 15 * time.Minute

// workspaceSyncPassLog carries counts, never paths: one pass is one line.
type workspaceSyncPassLog struct {
	params       WorkspaceSyncParams
	notGitRepo   bool
	remote       int
	stale        int
	staleUnknown bool
	local        int
	fetch        int
	deleted      int
	signed       int
	signNote     string
	fetchErr     error
	deleteErr    error
	failure      error
}

func (l *workspaceSyncPassLog) recordResolved(resolved workspaceSyncPaths, err error) {
	l.notGitRepo = resolved.notGitRepo
	l.remote = len(resolved.remote)
	l.stale = resolved.stale
	l.staleUnknown = resolved.staleUnknown
	l.local = len(resolved.localMeta)
	l.failure = err
}

func (l *workspaceSyncPassLog) reportable() bool {
	return l.fetch > 0 || l.deleted > 0 || l.signed > 0 || l.stale > 0 ||
		strings.TrimSpace(l.signNote) != "" ||
		l.fetchErr != nil || l.deleteErr != nil || l.failure != nil
}

// quietState names a standing condition worth its own line even when nothing changed.
func (l *workspaceSyncPassLog) quietState() string {
	switch {
	case l.notGitRepo:
		return "notGitRepo"
	case l.staleUnknown:
		return "staleIndexUnknown"
	default:
		return ""
	}
}

func (l *workspaceSyncPassLog) line() string {
	return fmt.Sprintf("erun: workspace sync %s -> %s: notGitRepo=%t remote=%d staleIndex=%s mirror=%d fetch=%d deleted=%d signed=%d%s%s%s%s",
		l.params.RemotePath, l.params.LocalPath, l.notGitRepo, l.remote, l.staleLabel(), l.local, l.fetch, l.deleted, l.signed,
		workspaceSyncPassNoteSuffix(" signNote", l.signNote),
		workspaceSyncPassErrorSuffix(" fetchError", l.fetchErr),
		workspaceSyncPassErrorSuffix(" deleteError", l.deleteErr),
		workspaceSyncPassErrorSuffix(" error", l.failure))
}

func (l *workspaceSyncPassLog) staleLabel() string {
	if l.staleUnknown {
		return "unknown"
	}
	return strconv.Itoa(l.stale)
}

func workspaceSyncPassNoteSuffix(label, note string) string {
	if strings.TrimSpace(note) == "" {
		return ""
	}
	return label + "=" + strings.TrimSpace(note)
}

func workspaceSyncPassErrorSuffix(label string, err error) string {
	if err == nil {
		return ""
	}
	return label + "=" + strings.TrimSpace(err.Error())
}

// workspaceSyncPassSink is process-wide because several mirrors share one log file.
type workspaceSyncPassSink struct {
	mu          sync.Mutex
	now         func() time.Time
	window      time.Duration
	logf        func(format string, args ...any)
	windowStart time.Time
	quiet       int
	mirrors     map[string]struct{}
	states      map[string]string
}

func newWorkspaceSyncPassSink() *workspaceSyncPassSink {
	return &workspaceSyncPassSink{
		now:     time.Now,
		window:  workspaceSyncLogWindow,
		logf:    log.Printf,
		mirrors: make(map[string]struct{}),
		states:  make(map[string]string),
	}
}

var workspaceSyncPasses = newWorkspaceSyncPassSink()

func (s *workspaceSyncPassSink) record(pass *workspaceSyncPassLog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.windowStart.IsZero() {
		s.windowStart = now
	}
	mirror := pass.params.LocalPath
	if pass.reportable() {
		// Report suppressed passes first so the log stays in order.
		s.flushLocked(now)
		s.logf("%s", pass.line())
		return
	}
	s.recordQuietLocked(pass, mirror)
	s.quiet++
	s.mirrors[mirror] = struct{}{}
	if now.Sub(s.windowStart) >= s.window {
		s.flushLocked(now)
	}
}

func (s *workspaceSyncPassSink) recordQuietLocked(pass *workspaceSyncPassLog, mirror string) {
	state := pass.quietState()
	if state == "" {
		delete(s.states, mirror)
		return
	}
	if s.states[mirror] == state {
		return
	}
	s.states[mirror] = state
	s.logf("%s", pass.line())
}

// flushLocked logs nothing for an empty window; a timer line would rebuild the flood.
func (s *workspaceSyncPassSink) flushLocked(now time.Time) {
	if s.quiet > 0 {
		elapsed := now.Sub(s.windowStart).Round(time.Second)
		s.logf("erun: workspace sync: %s in the last %s across %s",
			workspaceSyncNoOpPassCount(s.quiet), elapsed, workspaceSyncMirrorCount(len(s.mirrors)))
	}
	s.quiet = 0
	s.mirrors = make(map[string]struct{})
	s.states = make(map[string]string)
	s.windowStart = now
}

func workspaceSyncNoOpPassCount(count int) string {
	if count == 1 {
		return "1 no-op pass"
	}
	return strconv.Itoa(count) + " no-op passes"
}

func workspaceSyncMirrorCount(count int) string {
	if count == 1 {
		return "1 mirror"
	}
	return strconv.Itoa(count) + " mirrors"
}
