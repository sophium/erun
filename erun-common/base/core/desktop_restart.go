package core

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

	"github.com/sophium/erun/erun-common/base/model"
)

// A CLI restart cannot reimplement the desktop's RestartApp; it finds the running
// desktop through its control marker and asks it over loopback HTTP.
const (
	desktopControlMarkerFileName = "desktop-control.json"
	// DesktopControlPath is shared by erun-ui (listener) and erun-common (caller).
	DesktopControlPath = "/__erun_restart"
	// DesktopControlPlanPath is a separate path, not a dryRun flag: an older desktop
	// would ignore the flag and actually restart, but answers an unknown path with 404.
	DesktopControlPlanPath = DesktopControlPath + "/plan"
)

// DesktopControlMarker is written by a running desktop at startup and removed on
// clean shutdown. A crash leaves a pid that DesktopProcessAlive tells apart from a live one.
type DesktopControlMarker struct {
	PID           int   `json:"pid"`
	ControlPort   int   `json:"controlPort"`
	StartedAtUnix int64 `json:"startedAtUnix"`
}

// DefaultDesktopControlMarkerPath is the marker's location under UserConfigDir()/ERun.
func DefaultDesktopControlMarkerPath() string {
	dir := DefaultDesktopIdentityDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, desktopControlMarkerFileName)
}

// ReadDesktopControlMarker reads a desktop's marker; callers decide what a missing one means.
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

// DesktopRestartRequest names the orchestrator being resumed; the path selects the question.
type DesktopRestartRequest struct {
	OrchestratorID string `json:"orchestratorId"`
}

// DesktopRestartResponse answers both the restart and the plan path.
type DesktopRestartResponse struct {
	OK      bool                         `json:"ok"`
	Error   string                       `json:"error,omitempty"`
	Preview []model.DesktopRestartReopen `json:"preview,omitempty"`
}

// DesktopRestartDeps is RestartDesktopApp's test seam.
type DesktopRestartDeps struct {
	MarkerPath   string
	ReadMarker   func(string) (DesktopControlMarker, error)
	ProcessAlive func(int) bool
	// Post returns the desktop's answer separately from a transport error.
	Post func(ctx context.Context, controlPort int, orchestratorID string) (DesktopRestartResponse, error)
	Plan func(ctx context.Context, controlPort int, orchestratorID string) (DesktopRestartResponse, error)
}

func DefaultDesktopRestartDeps() DesktopRestartDeps {
	return DesktopRestartDeps{
		MarkerPath:   DefaultDesktopControlMarkerPath(),
		ReadMarker:   ReadDesktopControlMarker,
		ProcessAlive: DesktopProcessAlive,
		Post:         postDesktopRestart,
		Plan:         postDesktopRestartPlan,
	}
}

// RestartDesktopApp asks the running desktop to restart itself after verifying it
// is alive; it never spawns or signals a process itself. A dry run asks the plan
// path instead and never falls back to a restart.
func RestartDesktopApp(ctx context.Context, deps DesktopRestartDeps, orchestratorID string, dryRun bool) model.DesktopRestartOutcome {
	marker, err := deps.ReadMarker(deps.MarkerPath)
	if err != nil {
		reason := "no desktop app is currently running"
		if !os.IsNotExist(err) {
			// An unreadable marker is a real problem, unlike a missing one.
			reason = fmt.Sprintf("its restart control record could not be read (%v)", err)
		}
		return model.DesktopRestartOutcome{Status: model.DesktopRestartRefused, Reason: reason}
	}
	if !deps.ProcessAlive(marker.PID) {
		// A marker naming a dead pid identifies nothing to ask; reopening the desktop
		// reclaims it, so the reason says that.
		return model.DesktopRestartOutcome{
			Status: model.DesktopRestartRefused,
			Reason: fmt.Sprintf(
				"the desktop app recorded at pid %d is not running; the record is stale — quit and reopen the desktop app so it records a fresh one",
				marker.PID),
			PID:         marker.PID,
			ControlPort: marker.ControlPort,
		}
	}
	if dryRun {
		// Never fall through to the restart from a dry run.
		response, err := deps.Plan(ctx, marker.ControlPort, orchestratorID)
		outcome := model.DesktopRestartOutcome{Status: model.DesktopRestartWouldRestart, PID: marker.PID, ControlPort: marker.ControlPort}
		switch {
		case err != nil:
			outcome.PreviewUnavailable = restartPlanUnavailable(err.Error())
		case !response.OK:
			outcome.PreviewUnavailable = restartPlanUnavailable(response.Error)
		default:
			outcome.Preview = response.Preview
		}
		return outcome
	}
	response, err := deps.Post(ctx, marker.ControlPort, orchestratorID)
	if err != nil {
		return model.DesktopRestartOutcome{
			Status:      model.DesktopRestartRefused,
			Reason:      fmt.Sprintf("could not reach the running desktop app's restart control endpoint: %v", err),
			PID:         marker.PID,
			ControlPort: marker.ControlPort,
		}
	}
	if !response.OK {
		return model.DesktopRestartOutcome{Status: model.DesktopRestartFailed, Reason: response.Error, PID: marker.PID, ControlPort: marker.ControlPort}
	}
	return model.DesktopRestartOutcome{Status: model.DesktopRestartRestarted, PID: marker.PID, ControlPort: marker.ControlPort}
}

func restartPlanUnavailable(cause string) string {
	return "could not ask the running desktop app which orchestrators it would reopen: " + cause
}

func postDesktopRestart(ctx context.Context, controlPort int, orchestratorID string) (DesktopRestartResponse, error) {
	return postDesktopControl(ctx, controlPort, DesktopControlPath, orchestratorID)
}

func postDesktopRestartPlan(ctx context.Context, controlPort int, orchestratorID string) (DesktopRestartResponse, error) {
	return postDesktopControl(ctx, controlPort, DesktopControlPlanPath, orchestratorID)
}

// postDesktopControl reports an unserved path as such: that is how an older
// desktop safely refuses a dry run.
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
