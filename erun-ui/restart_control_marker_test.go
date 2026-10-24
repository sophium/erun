package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
)

func TestClaimDesktopControlMarker_RefusesToClobberALiveOwnersRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop-control.json")
	owner := core.DesktopControlMarker{PID: 50193, ControlPort: 56272, StartedAtUnix: 100}
	if _, err := claimDesktopControlMarker(path, owner, neverAlive); err != nil {
		t.Fatalf("claimDesktopControlMarker (owner): %v", err)
	}
	ownerAlive := func(pid int) bool { return pid == owner.PID }

	claim, err := claimDesktopControlMarker(path, core.DesktopControlMarker{PID: 92201, ControlPort: 56614, StartedAtUnix: 200}, ownerAlive)
	if err != nil {
		t.Fatalf("claimDesktopControlMarker (second instance): %v", err)
	}
	if claim.Recorded {
		t.Fatal("a second instance must not take the record from a live owner")
	}
	if claim.HolderPID != owner.PID {
		t.Fatalf("HolderPID = %d, want the live owner %d", claim.HolderPID, owner.PID)
	}
	got, err := core.ReadDesktopControlMarker(path)
	if err != nil {
		t.Fatalf("read the owner's record: %v", err)
	}
	if got != owner {
		t.Fatalf("the live owner's record was overwritten: got %+v, want %+v", got, owner)
	}

	if err := removeDesktopControlMarker(path, 92201); err != nil {
		t.Fatalf("removeDesktopControlMarker: %v", err)
	}
	if got, err := core.ReadDesktopControlMarker(path); err != nil || got != owner {
		t.Fatalf("a non-owner's shutdown stranded the live desktop: got %+v, err %v", got, err)
	}
}

func TestClaimDesktopControlMarker_AdoptsAStaleRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop-control.json")
	stale := core.DesktopControlMarker{PID: 50193, ControlPort: 56272, StartedAtUnix: 100}
	if _, err := claimDesktopControlMarker(path, stale, neverAlive); err != nil {
		t.Fatalf("claimDesktopControlMarker (stale): %v", err)
	}
	fresh := core.DesktopControlMarker{PID: os.Getpid(), ControlPort: 56614, StartedAtUnix: 200}
	claim, err := claimDesktopControlMarker(path, fresh, func(pid int) bool { return pid == os.Getpid() })
	if err != nil {
		t.Fatalf("claimDesktopControlMarker (adopting): %v", err)
	}
	if !claim.Recorded {
		t.Fatalf("expected the stale record to be adopted, got %+v", claim)
	}
	if got, err := core.ReadDesktopControlMarker(path); err != nil || got != fresh {
		t.Fatalf("got %+v, err %v; want %+v", got, err, fresh)
	}
}

func TestClaimReadRemoveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "desktop-control.json")
	if _, err := core.ReadDesktopControlMarker(path); err == nil {
		t.Fatal("expected an error reading a record that was never written")
	}
	want := core.DesktopControlMarker{PID: 42, ControlPort: 4242, StartedAtUnix: 100}
	if claim, err := claimDesktopControlMarker(path, want, neverAlive); err != nil || !claim.Recorded {
		t.Fatalf("claimDesktopControlMarker: claim %+v, err %v", claim, err)
	}
	if got, err := core.ReadDesktopControlMarker(path); err != nil || got != want {
		t.Fatalf("got %+v, err %v; want %+v", got, err, want)
	}
	if err := removeDesktopControlMarker(path, want.PID); err != nil {
		t.Fatalf("removeDesktopControlMarker: %v", err)
	}
	if _, err := core.ReadDesktopControlMarker(path); err == nil {
		t.Fatal("expected an error reading the record after removal")
	}
	// Removal is idempotent: shutdown may run more than once.
	if err := removeDesktopControlMarker(path, want.PID); err != nil {
		t.Fatalf("removeDesktopControlMarker (already gone): %v", err)
	}
}

// A record with no live target reads as an existing desktop, so it is refused.
func TestClaimDesktopControlMarker_RefusesAnUnusableRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop-control.json")
	if _, err := claimDesktopControlMarker("", core.DesktopControlMarker{PID: 42, ControlPort: 4242}, neverAlive); err == nil {
		t.Fatal("expected a claim with no record path to be refused")
	}
	if _, err := claimDesktopControlMarker(path, core.DesktopControlMarker{PID: 42}, neverAlive); err == nil {
		t.Fatal("expected a claim naming no live target to be refused")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("an unusable claim must write nothing, got %v", err)
	}
}

func TestRemoveDesktopControlMarker_RemovesOnlyItsOwnRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop-control.json")
	writer := core.DesktopControlMarker{PID: 50193, ControlPort: 56272, StartedAtUnix: 100}
	if _, err := claimDesktopControlMarker(path, writer, neverAlive); err != nil {
		t.Fatalf("claimDesktopControlMarker: %v", err)
	}

	if err := removeDesktopControlMarker(path, 92201); err != nil {
		t.Fatalf("removeDesktopControlMarker (other pid): %v", err)
	}
	if got, err := core.ReadDesktopControlMarker(path); err != nil || got != writer {
		t.Fatalf("a record naming another pid was removed: got %+v, err %v", got, err)
	}

	if err := removeDesktopControlMarker(path, writer.PID); err != nil {
		t.Fatalf("removeDesktopControlMarker (own pid): %v", err)
	}
	if _, err := core.ReadDesktopControlMarker(path); err == nil {
		t.Fatal("expected the writer's own record to be removed")
	}

	// An unparseable record cannot be shown to be ours, so it is kept.
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("stage unreadable record: %v", err)
	}
	if err := removeDesktopControlMarker(path, writer.PID); err != nil {
		t.Fatalf("removeDesktopControlMarker (unreadable): %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("an unreadable record must be left alone, got %v", err)
	}
}

const desktopControlCrashHelperEnv = "ERUN_TEST_DESKTOP_CONTROL_CRASH_PATH"

// TestDesktopControlCrashHelperProcess is a re-exec helper, not a test.
func TestDesktopControlCrashHelperProcess(t *testing.T) {
	path := os.Getenv(desktopControlCrashHelperEnv)
	if path == "" {
		return
	}
	if _, err := claimDesktopControlMarker(path, core.DesktopControlMarker{PID: os.Getpid(), ControlPort: 59775, StartedAtUnix: 100}, core.DesktopProcessAlive); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "claim control marker: %v\n", err)
		os.Exit(3)
	}
	os.Exit(0)
}

// A crashed desktop's record must survive so a later trigger can tell "gone" from "never ran".
func TestDesktopControlMarker_UncleanExitLeavesAStaleRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop-control.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestDesktopControlCrashHelperProcess$")
	cmd.Env = append(os.Environ(), desktopControlCrashHelperEnv+"="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crash helper: %v\n%s", err, out)
	}
	deadPID := cmd.Process.Pid

	marker, err := core.ReadDesktopControlMarker(path)
	if err != nil {
		t.Fatalf("an unclean exit must leave the record behind: %v", err)
	}
	if marker.PID != deadPID {
		t.Fatalf("record names pid %d, want the crashed process %d", marker.PID, deadPID)
	}
	if core.DesktopProcessAlive(deadPID) {
		t.Fatalf("pid %d exited and must not read as alive", deadPID)
	}
	assertCrashedRecordReadsAsStale(t, path)

	next := core.DesktopControlMarker{PID: os.Getpid(), ControlPort: 56614, StartedAtUnix: 200}
	claim, err := claimDesktopControlMarker(path, next, core.DesktopProcessAlive)
	if err != nil || !claim.Recorded {
		t.Fatalf("the crashed desktop's record must be adoptable: claim %+v, err %v", claim, err)
	}
	if got, err := core.ReadDesktopControlMarker(path); err != nil || got != next {
		t.Fatalf("got %+v, err %v; want %+v", got, err, next)
	}
}

func assertCrashedRecordReadsAsStale(t *testing.T, path string) {
	t.Helper()
	posted := false
	outcome := core.RestartDesktopApp(context.Background(), core.DesktopRestartDeps{
		MarkerPath:   path,
		ReadMarker:   core.ReadDesktopControlMarker,
		ProcessAlive: core.DesktopProcessAlive,
		Post: func(context.Context, int, string) (core.DesktopRestartResponse, error) {
			posted = true
			return core.DesktopRestartResponse{OK: true}, nil
		},
	}, "orch-1", false)
	if posted {
		t.Fatal("must not post a restart to a dead desktop's record")
	}
	if outcome.Status != core.DesktopRestartRefused || !strings.Contains(outcome.Reason, "stale") {
		t.Fatalf("outcome = %+v, want the dead desktop's record refused as stale", outcome)
	}
}

func TestAppClaimRestartControlMarker_RefusesToRepublishAfterShutdown(t *testing.T) {
	markerPath := filepath.Join(t.TempDir(), "state", "desktop-control.json")
	app := restartControlTestApp(t, markerPath)

	app.releaseRestartControlMarker()
	_, err := app.claimRestartControlMarker(core.DesktopControlMarker{PID: os.Getpid(), ControlPort: 56614, StartedAtUnix: 100})
	if !errors.Is(err, errRestartControlReleased) {
		t.Fatalf("err = %v, want errRestartControlReleased", err)
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("a released instance republished a control record: %v", err)
	}
}
