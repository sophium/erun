package eruncommon

import (
	"bytes"
	"context"
	"encoding/json"
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
	// DesktopControlPlanPath is where a dry run asks what a restart would
	// reopen — and it is a path of its own rather than a flag on the one above
	// because of what a desktop that predates it would do with a flag. Unknown
	// JSON fields are ignored, so a `dryRun` flag sent to an older build would
	// be read as an ordinary restart: the desktop would restart itself and
	// answer exactly what a restart answers, turning the one command whose
	// whole promise is "nothing is performed" into the action it promised not
	// to take. An unserved path cannot be misread that way. The two answer
	// from the same process and the same state, so the plan a dry run reports
	// and the restart it describes still cannot disagree.
	DesktopControlPlanPath = DesktopControlPath + "/plan"
)

// DesktopControlMarker is what a running desktop app (erun-ui) records at
// startup and removes at a clean shutdown, so an external trigger can find it
// and verify it before acting. This package owns the contract — the shape, its
// location, reading it, and the liveness probe that tells a live record from a
// stale one — while the desktop owns the record itself: it is the only thing
// that writes one, and it keeps the rule that a record naming a live process is
// never overwritten or removed by another instance. A marker left behind by a
// crash still names a pid, which is exactly what lets a stale one be told apart
// from a live one: see DesktopProcessAlive.
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
	// Preview is the dry run's answer: every orchestrator the restart would
	// reopen, and the ones among them that do not come back to the conversation
	// their own session was working in (see DesktopRestartReopen.Notice). Empty
	// for a real restart — by the time one is asked for, it has happened.
	Preview []DesktopRestartReopen `json:"preview,omitempty"`
	// PreviewUnavailable says a dry run resolved and verified a live target but
	// could not read the plan from it. The dry run still reports
	// would-restart — that much the marker and the liveness probe established —
	// and this is what keeps the missing half from reading as "nothing would be
	// stranded", which is the one thing an absent preview must never say.
	PreviewUnavailable string `json:"previewUnavailable,omitempty"`
}

// DesktopRestartRequest is what a trigger sends the running desktop: which
// orchestrator the restart is resuming. Which QUESTION is being asked is the
// path it is sent to, not a field of this (see DesktopControlPlanPath).
type DesktopRestartRequest struct {
	OrchestratorID string `json:"orchestratorId"`
}

// DesktopRestartReopen is one orchestrator a restart reopens, and what it comes
// back on.
type DesktopRestartReopen struct {
	OrchestratorID string `json:"orchestratorId"`
	// ConversationID is the conversation the launch that follows the restart
	// resumes for this orchestrator.
	ConversationID string `json:"conversationId,omitempty"`
	// Notice is set exactly when that is NOT the conversation this
	// orchestrator's own session was working in — and it is the whole warning:
	// it names both conversations, so the operator can tell which work is about
	// to be left behind and attach it before the restart happens. Empty is the
	// ordinary case, and the only one that needs no reporting.
	Notice string `json:"notice,omitempty"`
}

// DesktopRestartResponse is what the desktop answers either trigger with:
// whether it did the thing, why not when it did not, and — on the plan path —
// the plan it was asked for. One shape serves both because one process answers
// both from one piece of state; each path fills the fields it has an answer for.
type DesktopRestartResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Preview is the plan: every orchestrator a restart triggered now would
	// reopen, in the order the open set records them.
	Preview []DesktopRestartReopen `json:"preview,omitempty"`
}

// DesktopRestartDeps lets RestartDesktopApp's target resolution and transport
// be substituted in tests without a real running desktop process or a real OS
// pid to probe.
type DesktopRestartDeps struct {
	MarkerPath   string
	ReadMarker   func(string) (DesktopControlMarker, error)
	ProcessAlive func(int) bool
	// Post asks the desktop at controlPort to restart itself, returning its
	// answer separately from a transport error (could not even reach it).
	Post func(ctx context.Context, controlPort int, orchestratorID string) (DesktopRestartResponse, error)
	// Plan asks the desktop at controlPort what a restart would reopen, over
	// the path reserved for that question (see DesktopControlPlanPath).
	Plan func(ctx context.Context, controlPort int, orchestratorID string) (DesktopRestartResponse, error)
}

// DefaultDesktopRestartDeps wires the real marker file, the real OS process
// check, and the real HTTP calls to the desktop's two control endpoints.
func DefaultDesktopRestartDeps() DesktopRestartDeps {
	return DesktopRestartDeps{
		MarkerPath:   DefaultDesktopControlMarkerPath(),
		ReadMarker:   ReadDesktopControlMarker,
		ProcessAlive: DesktopProcessAlive,
		Post:         postDesktopRestart,
		Plan:         postDesktopRestartPlan,
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
//
// A dry run asks that same process the one question that is still actionable
// BEFORE it restarts: which orchestrators it would reopen, and which of them
// would not come back to the conversation their own session was working in. The
// answer is the outcome's Preview, and it comes from the desktop rather than
// from this caller reading state back for the same reason the restart does --
// the resolution is the desktop's, and a second implementation of it here would
// be one more thing that can disagree with what the restart then does. Not
// being able to read it is reported (see PreviewUnavailable) rather than
// rendered as an empty plan, and it is never fallen back on to a restart: the
// question travels a path of its own so that a desktop too old to answer it
// refuses the request instead of performing the action the dry run forbids
// (see DesktopControlPlanPath).
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
		// The record names a process that is gone, so it identifies no desktop
		// this call could ask to restart — and because a desktop that never
		// took the record over publishes no endpoint, there is nothing else to
		// resolve it by. Refusing is therefore the honest answer, but a bare
		// "the record is stale" is a dead end: the record is not what the
		// operator acts on. A record naming a dead pid is free, so reopening
		// the desktop app claims it cleanly and the trigger works again, and
		// the reason says so rather than leaving them with no path back to a
		// rebuild.
		return DesktopRestartOutcome{
			Status: DesktopRestartRefused,
			Reason: fmt.Sprintf(
				"the desktop app recorded at pid %d is not running; the record is stale — quit and reopen the desktop app so it records a fresh one",
				marker.PID),
			PID:         marker.PID,
			ControlPort: marker.ControlPort,
		}
	}
	if dryRun {
		// The plan is a separate question on a separate path, so the only thing
		// this branch can do is read it or say it could not. It never falls
		// through to the restart: a dry run that restarted because its question
		// was not understood would be the action it promised not to take.
		response, err := deps.Plan(ctx, marker.ControlPort, orchestratorID)
		outcome := DesktopRestartOutcome{Status: DesktopRestartWouldRestart, PID: marker.PID, ControlPort: marker.ControlPort}
		switch {
		case err != nil:
			outcome.PreviewUnavailable = fmt.Sprintf("could not ask the running desktop app which orchestrators it would reopen: %v", err)
		case !response.OK:
			outcome.PreviewUnavailable = fmt.Sprintf("could not ask the running desktop app which orchestrators it would reopen: %s", response.Error)
		default:
			outcome.Preview = response.Preview
		}
		return outcome
	}
	response, err := deps.Post(ctx, marker.ControlPort, orchestratorID)
	if err != nil {
		return DesktopRestartOutcome{
			Status:      DesktopRestartRefused,
			Reason:      fmt.Sprintf("could not reach the running desktop app's restart control endpoint: %v", err),
			PID:         marker.PID,
			ControlPort: marker.ControlPort,
		}
	}
	if !response.OK {
		return DesktopRestartOutcome{Status: DesktopRestartFailed, Reason: response.Error, PID: marker.PID, ControlPort: marker.ControlPort}
	}
	return DesktopRestartOutcome{Status: DesktopRestartRestarted, PID: marker.PID, ControlPort: marker.ControlPort}
}

// postDesktopRestart is the real Post: one POST to the desktop's own loopback
// control server, which is the only thing in a position to call its own
// App.RestartApp.
func postDesktopRestart(ctx context.Context, controlPort int, orchestratorID string) (DesktopRestartResponse, error) {
	return postDesktopControl(ctx, controlPort, DesktopControlPath, orchestratorID)
}

// postDesktopRestartPlan is the real Plan: the same call to the same server, on
// the path reserved for the question that must never be mistaken for the
// restart (see DesktopControlPlanPath).
func postDesktopRestartPlan(ctx context.Context, controlPort int, orchestratorID string) (DesktopRestartResponse, error) {
	return postDesktopControl(ctx, controlPort, DesktopControlPlanPath, orchestratorID)
}

// postDesktopControl puts one request to the desktop's control server at path
// and decodes its answer. A path the desktop does not serve is reported as
// exactly that — the one response a request sent to reach a build that predates
// it will get, and the reason it is safe to send a dry run to a desktop this
// command has never met.
func postDesktopControl(ctx context.Context, controlPort int, path, orchestratorID string) (DesktopRestartResponse, error) {
	body, err := json.Marshal(DesktopRestartRequest{OrchestratorID: orchestratorID})
	if err != nil {
		return DesktopRestartResponse{}, err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", controlPort, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return DesktopRestartResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return DesktopRestartResponse{}, err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return DesktopRestartResponse{}, fmt.Errorf(
			"the running desktop app does not serve %s, so it is older than this command", path)
	}
	var decoded DesktopRestartResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return DesktopRestartResponse{}, fmt.Errorf("decode response from %s: %w", path, err)
	}
	if !decoded.OK && decoded.Error == "" {
		decoded.Error = fmt.Sprintf("the desktop app reported HTTP %d on %s", resp.StatusCode, path)
	}
	return decoded, nil
}
