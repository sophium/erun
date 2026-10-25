package activity

import (
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func aStaleShellSessionAge() time.Time { return time.Now().Add(-2 * staleShellDebounce) }

func addDeadShellSession(sessions *fakeSessions, serial int, startedAt time.Time) *fakeSession {
	registered := sessions.add(Session{
		Serial:    serial,
		Selection: uimodel.Selection{Tenant: "team", Environment: "dev"},
		Kind:      KindCommand,
		StartedAt: startedAt,
	})
	registered.dead = true
	return registered
}

func TestASessionWhoseProcessDiedIsReportedAsAStaleShell(t *testing.T) {
	sessions := newFakeSessions()
	service := newTestService(Deps{Sessions: sessions})
	addDeadShellSession(sessions, 42, aStaleShellSessionAge())

	service.reconcileStaleShellsOnce()
	entry, ok := service.queue.findByID(staleShellActivityID(42))
	if !ok {
		t.Fatal("expected a stale-shell queue entry for serial 42")
	}
	if !strings.Contains(entry.Error, "shell exited unexpectedly") {
		t.Fatalf("unexpected stale-shell entry error: %q", entry.Error)
	}
	if entry.Command != "command" || entry.Summary != "command team/dev" || entry.SessionID != "42" {
		t.Fatalf("unexpected stale-shell entry: %+v", entry)
	}
}

// A shell that forked before its parent exited is not stale.
func TestAFreshlyStartedSessionIsHeldOutOfTheStaleDetector(t *testing.T) {
	sessions := newFakeSessions()
	service := newTestService(Deps{Sessions: sessions})
	addDeadShellSession(sessions, 43, time.Now())

	service.reconcileStaleShellsOnce()
	if entry, ok := service.queue.findByID(staleShellActivityID(43)); ok {
		t.Fatalf("a freshly started session was reported stale: %+v", entry)
	}
}

func TestAStaleShellThatRecoversIsDismissed(t *testing.T) {
	sessions := newFakeSessions()
	service := newTestService(Deps{Sessions: sessions})
	registered := addDeadShellSession(sessions, 44, aStaleShellSessionAge())

	service.reconcileStaleShellsOnce()
	if _, ok := service.queue.findByID(staleShellActivityID(44)); !ok {
		t.Fatal("precondition: the detector must report the dead shell")
	}
	sessions.mu.Lock()
	registered.dead = false
	sessions.mu.Unlock()

	service.reconcileStaleShellsOnce()
	if entry, ok := service.queue.findByID(staleShellActivityID(44)); ok {
		t.Fatalf("a shell no longer dead stayed reported: %+v", entry)
	}
}

// Kill must mark the session closed so the next detector pass does not re-queue it.
func TestKillingAStaleSessionDoesNotQueueItAgain(t *testing.T) {
	sessions := newFakeSessions()
	service := newTestService(Deps{Sessions: sessions})
	addDeadShellSession(sessions, 51, aStaleShellSessionAge())

	service.reconcileStaleShellsOnce()
	if _, ok := service.queue.findByID(staleShellActivityID(51)); !ok {
		t.Fatalf("precondition: the detector must report the reaped shell before Kill is offered")
	}

	if !service.KillSession(51) {
		t.Fatal("KillSession(51) found no session to kill")
	}
	if _, ok := service.queue.findByID(staleShellActivityID(51)); ok {
		t.Fatalf("KillSession did not dismiss the entry it was pressed on")
	}

	service.reconcileStaleShellsOnce()
	if entry, ok := service.queue.findByID(staleShellActivityID(51)); ok {
		t.Fatalf("a session the operator killed was re-queued as stale: %q", entry.Error)
	}
}

// A Kill aimed at another serial must not suppress the report.
func TestKillSessionForAnUnknownSerialLeavesTheDetectorAlone(t *testing.T) {
	sessions := newFakeSessions()
	service := newTestService(Deps{Sessions: sessions})
	addDeadShellSession(sessions, 52, aStaleShellSessionAge())

	service.reconcileStaleShellsOnce()
	if _, ok := service.queue.findByID(staleShellActivityID(52)); !ok {
		t.Fatalf("precondition: the detector must report the dead shell")
	}
	if service.KillSession(9999) {
		t.Fatalf("KillSession reported a session for a serial it holds none for")
	}

	service.reconcileStaleShellsOnce()
	if _, ok := service.queue.findByID(staleShellActivityID(52)); !ok {
		t.Fatalf("a dead shell no Kill landed on must stay reported as stale")
	}
}
