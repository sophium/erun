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
// This file owns the DURABLE one: which orchestrators are open. It is written the
// moment a session starts and cleared the moment the operator stops it — never
// at shutdown, because a crash, a `pkill` or a reboot takes the desktop away
// without running any hook, and a record only a clean quit could write would be
// missing exactly when it is most needed. It carries no timestamp either: an
// orchestrator the operator left open is still the one they were in, however
// long ago that was.
//
// The record is a SET, not a single id: every orchestrator the operator had
// open comes back, not just the last one started. It is kept in recency order —
// each (re)start moves its id to the end — because that order is also how
// app_restart.go picks which one owns the terminal pane when no restart
// hand-off names one: the most recently (re)started orchestrator, on the theory
// that starting one is also how the operator ends up looking at it.
//
// app_restart.go owns the OTHER record: the one-shot hand-off an in-app restart
// writes, which carries the prompt the resumed session should auto-run. That one
// stays one-shot and age-bounded, so a rebuild+restart continues its task while
// a plain launch resumes the conversation idle at the prompt.

const orchestratorOpenFileName = "orchestrator-open.json"

type orchestratorOpenState struct {
	// OrchestratorIDs is the set of orchestrators open when the desktop was last
	// running, oldest first.
	OrchestratorIDs []string `json:"orchestratorIds,omitempty"`
	// OrchestratorID is the shape this file had before it could hold more than
	// one id. Only read, never written again: an operator upgrading from a
	// release that only ever wrote this field must not lose the one
	// orchestrator they had open.
	OrchestratorID string `json:"orchestratorId,omitempty"`
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

// recordOpenOrchestrator adds an orchestrator to the open set, or moves it to
// the end (most recent) if it was already there.
func recordOpenOrchestrator(path, orchestratorID string) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	ids := readOpenOrchestrators(path)
	out := make([]string, 0, len(ids)+1)
	for _, id := range ids {
		if id != orchestratorID {
			out = append(out, id)
		}
	}
	out = append(out, orchestratorID)
	return writeOpenOrchestrators(path, out)
}

// clearOpenOrchestrator forgets one orchestrator when the operator stops it,
// which is what keeps an explicitly stopped orchestrator closed on every later
// launch. Every other id in the set is left exactly as recorded: stopping one
// orchestrator must not forget that the rest are still open.
func clearOpenOrchestrator(path, orchestratorID string) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	ids := readOpenOrchestrators(path)
	out := make([]string, 0, len(ids))
	removed := false
	for _, id := range ids {
		if id == orchestratorID {
			removed = true
			continue
		}
		out = append(out, id)
	}
	if !removed {
		return nil
	}
	return writeOpenOrchestrators(path, out)
}

// readOpenOrchestrators returns the orchestrators that were open when the
// desktop last ran, oldest first, or nil when none were. Reading does not clear
// the record: it is durable, so every launch reopens the same set until an
// entry is stopped. A legacy single-id file is understood as a one-element set.
func readOpenOrchestrators(path string) []string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state orchestratorOpenState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(state.OrchestratorIDs)+1)
	ids := make([]string, 0, len(state.OrchestratorIDs)+1)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	// The legacy scalar is the sole open orchestrator on a file no launch has
	// rewritten into the set shape yet, so it takes the oldest position.
	add(state.OrchestratorID)
	for _, id := range state.OrchestratorIDs {
		add(id)
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// writeOpenOrchestrators persists the open set, migrating a legacy scalar file
// to the set shape the first time anything changes. An empty set removes the
// file rather than leaving a durable record with nothing durable to say.
func writeOpenOrchestrators(path string, ids []string) error {
	if path == "" {
		return nil
	}
	if len(ids) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(orchestratorOpenState{OrchestratorIDs: ids})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
