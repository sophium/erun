package eruncommon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// The desktop app's Restart button (erun-ui's App.RestartApp) is the only
// place that knows how to hand a rebuild+restart off correctly: it resolves
// which conversation is actually live for the orchestrator asking, writes the
// resume hand-off, relaunches a fresh copy of itself, and quits — all inside
// the one process that holds that live session state in memory. Nothing
// outside that process can reconstruct it, so a CLI-triggered restart cannot
// reimplement the restart; it can only ask the running desktop to run its own
// RestartApp. desktopControlMarkerFileName is where the running desktop
// records how to reach itself for exactly that ask, and DesktopControlPath is
// the endpoint RestartDesktopApp calls once the marker resolves to a live pid.
const (
	desktopControlMarkerFileName = "desktop-control.json"
	// DesktopControlPath is the loopback HTTP path the desktop's control
	// server listens on for a restart trigger. Exported so erun-ui, which owns
	// the listener, and erun-common, which owns the caller, agree on it
	// without one importing the other.
	DesktopControlPath = "/__erun_restart"
)

// DesktopControlMarker is what a running desktop app (erun-ui) records at
// startup and removes at a clean shutdown, so an external trigger can find it
// and verify it before acting. A marker left behind by a crash still names a
// pid, which is exactly what lets a stale one be told apart from a live one:
// see DesktopProcessAlive.
type DesktopControlMarker struct {
	PID           int   `json:"pid"`
	ControlPort   int   `json:"controlPort"`
	StartedAtUnix int64 `json:"startedAtUnix"`
}

// DefaultDesktopControlMarkerPath is the one location every erun-app instance
// writes to and every restart trigger reads from, beside the desktop's other
// per-installation state under UserConfigDir()/ERun.
func DefaultDesktopControlMarkerPath() string {
	dir := DefaultDesktopIdentityDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, desktopControlMarkerFileName)
}

// WriteDesktopControlMarker persists marker at path, creating its directory if
// needed. Called once by the desktop at startup.
func WriteDesktopControlMarker(path string, marker DesktopControlMarker) error {
	if path == "" {
		return fmt.Errorf("desktop control marker path is unset")
	}
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ReadDesktopControlMarker reads what a running (or previously running)
// desktop app recorded. The caller decides what an unreadable or missing
// marker means; it usually means no desktop app has ever started, or one
// exited cleanly and removed its own marker.
func ReadDesktopControlMarker(path string) (DesktopControlMarker, error) {
	if path == "" {
		return DesktopControlMarker{}, fmt.Errorf("desktop control marker path is unset")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DesktopControlMarker{}, err
	}
	var marker DesktopControlMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return DesktopControlMarker{}, fmt.Errorf("parse desktop control marker %s: %w", path, err)
	}
	if marker.PID <= 0 || marker.ControlPort <= 0 {
		return DesktopControlMarker{}, fmt.Errorf("desktop control marker %s names no live target", path)
	}
	return marker, nil
}

// RemoveDesktopControlMarker deletes a marker a clean shutdown no longer
// vouches for. A missing file is not an error: shutdown may run twice, or the
// marker may never have been written (a build with no network access to bind
// the control listener).
func RemoveDesktopControlMarker(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ErrDesktopControlMarkerHeld is what ClaimDesktopControlMarker returns when
// an existing marker names a different pid that is still alive: writing over
// it would strand that live instance's own control record.
var ErrDesktopControlMarkerHeld = errors.New("desktop control marker already claimed by a live instance")

// ClaimDesktopControlMarker writes marker at path unless doing so would
// overwrite a different, currently-alive instance's own entry. A missing,
// unreadable, or stale marker (one naming a pid processAlive reports as gone)
// is always claimed freely; a marker naming this same pid is always
// refreshed, since a running instance re-asserting its own entry can never
// strand itself.
//
// Before this existed, every launch wrote its own pid/port over whatever was
// there, so a transient second instance -- started by accident, or racing
// the first by a few seconds -- could silently take over the control record
// of an already-running desktop. Both the one-time startup write and the
// periodic self-heal on erun-ui's existing session-heartbeat reconciler tick
// (see reconcileDesktopControlMarker) go through this one path, so whichever
// instance is genuinely running is the one the marker ends up naming.
func ClaimDesktopControlMarker(path string, marker DesktopControlMarker, processAlive func(int) bool) error {
	if existing, err := ReadDesktopControlMarker(path); err == nil {
		if existing.PID != marker.PID && processAlive(existing.PID) {
			return fmt.Errorf("%w: pid %d", ErrDesktopControlMarkerHeld, existing.PID)
		}
	}
	return WriteDesktopControlMarker(path, marker)
}

// ReleaseDesktopControlMarker removes the marker at path only if it still
// names pid, the caller's own. This is the other half of the same fix: an
// instance whose own claim was refused by ClaimDesktopControlMarker never
// wrote the marker in the first place, so its shutdown must not delete it
// either -- an unconditional remove there would strand the live instance on
// exit just as badly as an unconditional write stranded it at startup.
func ReleaseDesktopControlMarker(path string, pid int) error {
	existing, err := ReadDesktopControlMarker(path)
	if err != nil {
		return nil
	}
	if existing.PID != pid {
		return nil
	}
	return RemoveDesktopControlMarker(path)
}

// DesktopRestartStatus is the outcome of one RestartDesktopApp call, reported
// back to the caller so success, refusal, and failure are never conflated. See
// root AGENTS.md "Smooth, Seamless, No Dead Ends": an action that silently did
// nothing is the exact failure this exists to prevent.
type DesktopRestartStatus string

const (
	// DesktopRestartWouldRestart is the dry-run answer: a live target resolved
	// and would have been asked to restart.
	DesktopRestartWouldRestart DesktopRestartStatus = "would-restart"
	// DesktopRestartRestarted means the running desktop app accepted the
	// restart request and is now relaunching itself.
	DesktopRestartRestarted DesktopRestartStatus = "restarted"
	// DesktopRestartRefused means no safe target could be resolved and
	// verified, so nothing was attempted. This is the pid-does-not-resolve
	// case root AGENTS.md calls out: a relauncher armed against a dead target
	// kills nothing and a following relaunch just re-activates what was
	// already there, which reads exactly like a restart that did not take.
	DesktopRestartRefused DesktopRestartStatus = "refused"
	// DesktopRestartFailed means a live target was resolved and reached, but
	// it reported that the restart itself did not succeed.
	DesktopRestartFailed DesktopRestartStatus = "failed"
)

// DesktopRestartOutcome is RestartDesktopApp's result: what was decided, why,
// and which target it decided about.
type DesktopRestartOutcome struct {
	Status      DesktopRestartStatus `json:"status"`
	Reason      string               `json:"reason,omitempty"`
	PID         int                  `json:"pid,omitempty"`
	ControlPort int                  `json:"controlPort,omitempty"`
}

// DesktopRestartDeps lets RestartDesktopApp's target resolution and transport
// be substituted in tests without a real running desktop process or a real OS
// pid to probe.
type DesktopRestartDeps struct {
	MarkerPath   string
	ReadMarker   func(string) (DesktopControlMarker, error)
	ProcessAlive func(int) bool
	// Post asks the desktop at controlPort to restart, returning the remote
	// call's own outcome (ok, and a reason when it is not) separately from a
	// transport error (could not even reach it).
	Post func(ctx context.Context, controlPort int, orchestratorID string) (ok bool, reason string, err error)
}

// DefaultDesktopRestartDeps wires the real marker file, the real OS process
// check, and a real HTTP call to the desktop's control endpoint.
func DefaultDesktopRestartDeps() DesktopRestartDeps {
	return DesktopRestartDeps{
		MarkerPath:   DefaultDesktopControlMarkerPath(),
		ReadMarker:   ReadDesktopControlMarker,
		ProcessAlive: DesktopProcessAlive,
		Post:         postDesktopRestart,
	}
}

// RestartDesktopApp triggers the one restart mechanism the desktop app owns
// (App.RestartApp) from outside its process: resolve the running desktop from
// its control marker, verify it is actually alive before touching anything,
// then — unless dryRun — ask it to restart itself. It never spawns a second
// desktop instance itself and never signals a pid directly: the running
// desktop is the only thing that can correctly write the resume hand-off (it
// alone holds which conversation is live for orchestratorID), relaunch a
// fresh copy, and quit, so this always defers to that single mechanism rather
// than growing a second one beside it.
func RestartDesktopApp(ctx context.Context, deps DesktopRestartDeps, orchestratorID string, dryRun bool) DesktopRestartOutcome {
	marker, err := deps.ReadMarker(deps.MarkerPath)
	if err != nil {
		reason := "no desktop app is currently running"
		if !os.IsNotExist(err) {
			// A marker that exists but cannot be read or parsed is a genuine
			// problem (corrupt state, a permissions issue), not the ordinary
			// "nothing running" case, so its detail is worth keeping.
			reason = fmt.Sprintf("its restart control record could not be read (%v)", err)
		}
		return DesktopRestartOutcome{Status: DesktopRestartRefused, Reason: reason}
	}
	if !deps.ProcessAlive(marker.PID) {
		return DesktopRestartOutcome{
			Status:      DesktopRestartRefused,
			Reason:      fmt.Sprintf("the desktop app recorded at pid %d is not running; the record is stale", marker.PID),
			PID:         marker.PID,
			ControlPort: marker.ControlPort,
		}
	}
	if dryRun {
		return DesktopRestartOutcome{Status: DesktopRestartWouldRestart, PID: marker.PID, ControlPort: marker.ControlPort}
	}
	ok, reason, err := deps.Post(ctx, marker.ControlPort, orchestratorID)
	if err != nil {
		return DesktopRestartOutcome{
			Status:      DesktopRestartRefused,
			Reason:      fmt.Sprintf("could not reach the running desktop app's restart control endpoint: %v", err),
			PID:         marker.PID,
			ControlPort: marker.ControlPort,
		}
	}
	if !ok {
		return DesktopRestartOutcome{Status: DesktopRestartFailed, Reason: reason, PID: marker.PID, ControlPort: marker.ControlPort}
	}
	return DesktopRestartOutcome{Status: DesktopRestartRestarted, PID: marker.PID, ControlPort: marker.ControlPort}
}

// desktopRestartRequest/desktopRestartResponse are the wire shapes for
// DesktopControlPath, shared so erun-ui's handler and postDesktopRestart agree
// without one module importing the other's package.
type desktopRestartRequest struct {
	OrchestratorID string `json:"orchestratorId"`
}

type desktopRestartResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// postDesktopRestart is the real Post: one POST to the desktop's own loopback
// control server, which is the only thing in a position to call its own
// App.RestartApp.
func postDesktopRestart(ctx context.Context, controlPort int, orchestratorID string) (bool, string, error) {
	body, err := json.Marshal(desktopRestartRequest{OrchestratorID: orchestratorID})
	if err != nil {
		return false, "", err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", controlPort, DesktopControlPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, "", err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	var decoded desktopRestartResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return false, "", fmt.Errorf("decode restart response: %w", err)
	}
	if !decoded.OK {
		reason := decoded.Error
		if reason == "" {
			reason = fmt.Sprintf("the desktop app reported HTTP %d", resp.StatusCode)
		}
		return false, reason, nil
	}
	return true, "", nil
}
