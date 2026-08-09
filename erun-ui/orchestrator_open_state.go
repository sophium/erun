package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The desktop keeps two separate records of which orchestrator a launch should
// reopen, because they answer different questions and must not share a lifetime.
//
// This file owns the DURABLE one: which orchestrator is open. It is written the
// moment a session starts and cleared the moment the operator stops it — never
// at shutdown, because a crash, a `pkill` or a reboot takes the desktop away
// without running any hook, and a record only a clean quit could write would be
// missing exactly when it is most needed. It carries no timestamp either: an
// orchestrator the operator left open is still the one they were in, however
// long ago that was.
//
// app_restart.go owns the OTHER one: the one-shot hand-off an in-app restart
// writes, which carries the prompt the resumed session should auto-run. That one
// stays one-shot and age-bounded, so a rebuild+restart continues its task while
// a plain launch resumes the conversation idle at the prompt.

const orchestratorOpenFileName = "orchestrator-open.json"

type orchestratorOpenState struct {
	OrchestratorID string `json:"orchestratorId"`
}

// defaultOrchestratorOpenPath is a sibling of window-state.json under
// UserConfigDir()/ERun: desktop session state the app restores itself with.
func defaultOrchestratorOpenPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", orchestratorOpenFileName)
}

func recordOpenOrchestrator(path, orchestratorID string) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	data, err := json.Marshal(orchestratorOpenState{OrchestratorID: orchestratorID})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// clearOpenOrchestrator forgets the open orchestrator when the operator stops
// the one that is recorded, which is what keeps an explicitly stopped
// orchestrator closed on every later launch. Stopping some other orchestrator
// leaves the record alone: it still names the one that owns the pane.
func clearOpenOrchestrator(path, orchestratorID string) error {
	if path == "" || readOpenOrchestrator(path) != strings.TrimSpace(orchestratorID) {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// readOpenOrchestrator returns the orchestrator that was open when the desktop
// last ran, or "" when none was. Reading does not clear it: the record is
// durable, so every launch reopens the same orchestrator until it is stopped.
func readOpenOrchestrator(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var state orchestratorOpenState
	if err := json.Unmarshal(data, &state); err != nil {
		return ""
	}
	return strings.TrimSpace(state.OrchestratorID)
}
