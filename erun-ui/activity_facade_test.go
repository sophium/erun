package main

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	activitysvc "github.com/sophium/erun/erun-ui/internal/activity"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func newActivityTestApp() *App {
	app := withTestTerminal(&App{deps: erunUIDeps{store: stubUIStore{}}})
	app.activity = app.newActivityService()
	return app
}

// The activity service names session kinds by value; a renamed terminal kind
// would silently stop the locks, the trace and the stale detector matching it.
func TestActivitySessionKindsAreTheTerminals(t *testing.T) {
	kinds := map[string]terminal.Kind{
		activitysvc.KindOpen:           terminal.KindOpen,
		activitysvc.KindLocal:          terminal.KindLocal,
		activitysvc.KindAI:             terminal.KindAI,
		activitysvc.KindCommand:        terminal.KindCommand,
		activitysvc.KindOrchestrator:   terminal.KindOrchestrator,
		activitysvc.KindContributeERun: terminal.KindContributeERun,
		activitysvc.KindContributeAI:   terminal.KindContributeAI,
	}
	for name, kind := range kinds {
		if name != string(kind) {
			t.Fatalf("activity kind %q does not match terminal kind %q", name, kind)
		}
	}
}

// Output must be buffered before the failure line finalizes the entry.
func TestFeedActivityTraceCapturesFailureDetail(t *testing.T) {
	app := newActivityTestApp()
	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	managed := &terminal.Session{Selection: selection, Kind: terminal.KindLocal, Key: "local\x00team\x00dev", Serial: 1}
	setTestSession(app, managed.Key, managed)

	// Seeded directly: the trace start path needs env config this test does not stage.
	app.activity.Track(activitysvc.Entry{
		Command:     "deploy",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
	})

	feed := func(s string) { app.feedActivityTraceFromTerminal(managed, []byte(s)) }
	feed("helm upgrade --install team-devops ./chart\r\n")
	feed("Error: UPGRADE FAILED: timed out waiting for the condition\r\n")
	feed("==> Deploy failed after 4s\r\n")

	if active := app.FindActiveDeployForSelection(selection); active.ID != "" {
		t.Fatal("entry should be finalized out of active after the failure line")
	}
	var failed *activitysvc.Entry
	for _, entry := range app.ListDeploys() {
		if entry.Tenant == "team" && entry.Environment == "dev" {
			e := entry
			failed = &e
			break
		}
	}
	if failed == nil {
		t.Fatal("expected a finalized entry for team/dev")
	}
	if failed.Status != activitysvc.StatusFailed {
		t.Fatalf("status = %q, want failed", failed.Status)
	}
	for _, want := range []string{"helm upgrade --install", "UPGRADE FAILED", "==> Deploy failed after 4s"} {
		if !strings.Contains(failed.Detail, want) {
			t.Fatalf("Detail missing %q, got %q", want, failed.Detail)
		}
	}
}

// staleShellAge stands in for the detector's debounce.
const staleShellAge = 20 * time.Second

func aStaleShellSessionAge() time.Time { return time.Now().Add(-2 * staleShellAge) }

// addStaleShellSession runs no reader goroutine so the closed state stays deterministic.
func addStaleShellSession(app *App, serial int, startedAt time.Time) (*terminal.Session, *stubTerminalSession) {
	session := newStubTerminalSession()
	session.pid = serial
	return registerShellSession(app, serial, startedAt, session), session
}

// reapedShellSession's process is gone while its PTY is still open and registered.
type reapedShellSession struct {
	*stubTerminalSession
	mu    sync.Mutex
	alive bool
}

func (s *reapedShellSession) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alive
}

// Close does not resurrect the process.
func (s *reapedShellSession) Close() error {
	return s.stubTerminalSession.Close()
}

func addReapedShellSession(app *App, serial int, startedAt time.Time) (*terminal.Session, *reapedShellSession) {
	session := &reapedShellSession{stubTerminalSession: newSilentStubTerminalSession()}
	session.pid = serial
	return registerShellSession(app, serial, startedAt, session), session
}

func registerShellSession(app *App, serial int, startedAt time.Time, pty terminal.PTY) *terminal.Session {
	managed := &terminal.Session{
		PTY:       pty,
		Key:       "extra\x00" + strconv.Itoa(serial),
		Serial:    serial,
		Kind:      terminal.KindCommand,
		StartedAt: startedAt,
	}
	l, unlock := lockTerminalForTest(app)
	l.Register(managed)
	unlock()
	return managed
}

// A session the desktop closed itself must not look like one that died on its own.
func TestADeliberatelyClosedSessionIsNotReportedAsDead(t *testing.T) {
	app := newActivityTestApp()
	managed, session := addStaleShellSession(app, 41, aStaleShellSessionAge())

	if err := app.CloseSession(managed.Serial); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	// Precondition: the process really is gone.
	if session.Alive() {
		t.Fatalf("CloseSession left the session's process alive")
	}
	if dead := (activitySessions{a: app}).DeadSessions(staleShellAge); len(dead) != 0 {
		t.Fatalf("a session the operator just closed was offered to the stale detector: %+v", dead)
	}
}

func TestASessionWhoseProcessDiedOnItsOwnIsReportedDead(t *testing.T) {
	app := newActivityTestApp()
	managed, session := addStaleShellSession(app, 42, aStaleShellSessionAge())

	// Kill the process without any deliberate-close path claiming the exit.
	if err := session.Close(); err != nil {
		t.Fatalf("stub Close: %v", err)
	}
	dead := (activitySessions{a: app}).DeadSessions(staleShellAge)
	if len(dead) != 1 || dead[0].Serial != managed.Serial || dead[0].Kind != activitysvc.KindCommand {
		t.Fatalf("a session that died on its own must still be reported dead, got %+v", dead)
	}
}

// A shell that forked before its parent exited is not stale.
func TestAFreshlyStartedSessionIsNotReportedDead(t *testing.T) {
	app := newActivityTestApp()
	managed, session := addStaleShellSession(app, 43, time.Now())

	if err := app.CloseSession(managed.Serial); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if session.Alive() {
		t.Fatalf("CloseSession left the session's process alive")
	}
	if dead := (activitySessions{a: app}).DeadSessions(staleShellAge); len(dead) != 0 {
		t.Fatalf("a freshly started session was reported dead: %+v", dead)
	}
}

// Kill must mark the session closed so the next detector pass does not see it again.
func TestKillingADeadSessionClosesIt(t *testing.T) {
	app := newActivityTestApp()
	sessions := activitySessions{a: app}
	managed, session := addReapedShellSession(app, 51, aStaleShellSessionAge())

	if dead := sessions.DeadSessions(staleShellAge); len(dead) != 1 {
		t.Fatalf("precondition: the reaped shell must read dead before Kill is offered, got %+v", dead)
	}
	if !app.KillSession(managed.Serial) {
		t.Fatalf("KillSession(%d) found no session to kill", managed.Serial)
	}
	if session.Alive() {
		t.Fatalf("the session read alive after KillSession")
	}
	if dead := sessions.DeadSessions(staleShellAge); len(dead) != 0 {
		t.Fatalf("a session the operator killed still reads dead: %+v", dead)
	}
}

// A Kill aimed at another serial must not suppress the report.
func TestKillSessionForAnUnknownSerialLeavesTheDeadSessionAlone(t *testing.T) {
	app := newActivityTestApp()
	sessions := activitySessions{a: app}
	managed, _ := addReapedShellSession(app, 52, aStaleShellSessionAge())

	if app.KillSession(9999) {
		t.Fatalf("KillSession reported a session for a serial it holds none for")
	}
	dead := sessions.DeadSessions(staleShellAge)
	if len(dead) != 1 || dead[0].Serial != managed.Serial {
		t.Fatalf("a dead shell no Kill landed on must stay reported, got %+v", dead)
	}
}
