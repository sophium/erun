package eruncommon

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

// workspaceSyncLogWindow is how long a mirror may pass without a sync worth
// reporting before one aggregate line says the loop is still alive. It is what
// keeps silence meaningful: a mirror that syncs every few seconds and changes
// nothing must not cost a line every few seconds, and a stretch of log with no
// sync line at all must still be readable as "nothing changed", not as "the
// sync loop died". The retained log is a rotation-bounded resource, and a
// subsystem that fills it with "nothing happened" is what pushes a real event
// out of the window it would have been diagnosed in.
const workspaceSyncLogWindow = 15 * time.Minute

// workspaceSyncPassLog is the record of what one sync pass saw and did. Every
// count it carries is a count, never a path: one pass is one line no matter how
// many files it moved.
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

// reportable reports whether the pass has something of its own to say: it
// transferred, deleted, or signed files, saw a stale index, carried a signing
// note, or failed. Anything else is a no-op, and the sink folds those into its
// aggregate line instead of logging them one at a time.
func (l *workspaceSyncPassLog) reportable() bool {
	return l.fetch > 0 || l.deleted > 0 || l.signed > 0 || l.stale > 0 ||
		strings.TrimSpace(l.signNote) != "" ||
		l.fetchErr != nil || l.deleteErr != nil || l.failure != nil
}

// quietState names why a pass that changed nothing is still worth a line of its
// own — a mirror that is not a git repo, or a worktree listing that could not be
// read, is a standing condition an operator has to be able to see. It is empty
// for an ordinary in-step pass, which is exactly the case the aggregate line
// covers instead.
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

// line renders the pass as its one bounded diagnostic.
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

// workspaceSyncPassSink decides which passes reach the log and how often a
// quiet loop still says it is alive. One sink serves the process, because the
// cadence is a property of the log a host keeps rather than of one mirror: the
// desktop polls several environments at once and their passes share one file,
// so a per-pass line there is a per-pass line per environment.
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

// workspaceSyncPasses is the process-wide sink every finished pass reports to.
var workspaceSyncPasses = newWorkspaceSyncPassSink()

// record takes one finished pass: a reportable pass logs its own full line, and
// a quiet one is counted towards the next aggregate line.
func (s *workspaceSyncPassSink) record(pass *workspaceSyncPassLog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.windowStart.IsZero() {
		s.windowStart = now
	}
	mirror := pass.params.LocalPath
	if pass.reportable() {
		// Whatever was suppressed before this line is reported first, so the
		// log reads in the order the passes happened.
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

// recordQuietLocked says so when a mirror enters a quiet state that is itself
// the diagnosis, and otherwise stays silent.
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

// flushLocked reports the no-op passes suppressed since the window opened, and
// reopens the window. It logs nothing when there were none — the empty window
// is not an event, and saying so on a timer would rebuild the flood.
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
