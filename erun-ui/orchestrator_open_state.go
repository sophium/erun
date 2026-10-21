package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The desktop keeps a durable record of which orchestrators are open, so the
// next launch reattaches every one of them. It is written the moment a session
// starts and cleared the moment the operator stops it — never at shutdown,
// because a crash, a `pkill` or a reboot takes the desktop away without running
// any hook, and a record only a clean quit could write would be missing exactly
// when it is most needed. It carries no timestamp either: an orchestrator the
// operator left open is still the one they were in, however long ago that was.
//
// The record is a SET, not a single id: every orchestrator the operator had
// open comes back, not just the last one started. It is kept in recency order —
// each (re)start moves its entry to the end — because that order is also how
// app_restart.go picks which one owns the terminal pane when no restart
// hand-off names one: the most recently (re)started orchestrator, on the theory
// that starting one is also how the operator ends up looking at it.
//
// It names no conversation: each orchestrator's background agent keeps its
// own, and reattaching lands on it.

const orchestratorOpenFileName = "orchestrator-open.json"

// orchestratorOpenEntry is one orchestrator in the durable open set.
type orchestratorOpenEntry struct {
	OrchestratorID string `json:"orchestratorId"`
	// AttachedConversationID is a conversation the operator attached by hand
	// under an earlier release. It is never set any more, only carried through
	// rewrites until that orchestrator's first agent resumes it (see
	// orchestratorLegacyConversation).
	AttachedConversationID string `json:"attachedConversationId,omitempty"`
}

type orchestratorOpenState struct {
	// Orchestrators is the set of orchestrators open when the desktop was last
	// running, oldest first.
	Orchestrators []orchestratorOpenEntry `json:"orchestrators,omitempty"`
	// OrchestratorIDs is the shape this file had under an earlier release.
	// Only read, never written again, so an operator upgrading from that
	// release keeps the orchestrators they had open.
	OrchestratorIDs []string `json:"orchestratorIds,omitempty"`
	// OrchestratorID is the shape from a release before that, before the file
	// could hold more than one id at all. Same treatment: read, never written
	// again.
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
	entries := readOpenOrchestrators(path)
	out := make([]orchestratorOpenEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.OrchestratorID == orchestratorID {
			continue
		}
		out = append(out, entry)
	}
	// An orchestrator is recorded once its agent exists, and that agent already
	// continued any conversation attached under the earlier release.
	out = append(out, orchestratorOpenEntry{OrchestratorID: orchestratorID})
	return writeOpenOrchestrators(path, out)
}

// clearOpenOrchestrator forgets one orchestrator when the operator stops it,
// which is what keeps an explicitly stopped orchestrator closed on every later
// launch. Every other entry in the set is left exactly as recorded: stopping
// one orchestrator must not forget that the rest are still open.
func clearOpenOrchestrator(path, orchestratorID string) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	entries := readOpenOrchestrators(path)
	out := make([]orchestratorOpenEntry, 0, len(entries))
	removed := false
	for _, entry := range entries {
		if entry.OrchestratorID == orchestratorID {
			removed = true
			continue
		}
		out = append(out, entry)
	}
	if !removed {
		return nil
	}
	return writeOpenOrchestrators(path, out)
}

// readOpenOrchestrators returns the orchestrators that were open when the
// desktop last ran, oldest first. Reading does not clear the record: it is
// durable, so every launch reopens the same set until an entry is stopped.
func readOpenOrchestrators(path string) []orchestratorOpenEntry {
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
	if entries := dedupOrchestratorEntries(state.Orchestrators); len(entries) > 0 {
		return entries
	}
	legacy := make([]orchestratorOpenEntry, 0, len(state.OrchestratorIDs)+1)
	if id := strings.TrimSpace(state.OrchestratorID); id != "" {
		legacy = append(legacy, orchestratorOpenEntry{OrchestratorID: id})
	}
	for _, id := range state.OrchestratorIDs {
		legacy = append(legacy, orchestratorOpenEntry{OrchestratorID: id})
	}
	return dedupOrchestratorEntries(legacy)
}

// dedupOrchestratorEntries trims, drops empties, and keeps the first occurrence
// of each id, preserving order.
func dedupOrchestratorEntries(entries []orchestratorOpenEntry) []orchestratorOpenEntry {
	seen := make(map[string]struct{}, len(entries))
	out := make([]orchestratorOpenEntry, 0, len(entries))
	for _, entry := range entries {
		id := strings.TrimSpace(entry.OrchestratorID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, orchestratorOpenEntry{
			OrchestratorID:         id,
			AttachedConversationID: strings.TrimSpace(entry.AttachedConversationID),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// writeOpenOrchestrators persists the open set, migrating a legacy file to the
// current shape the first time anything changes. An empty set removes the file
// rather than leaving a durable record with nothing durable to say.
func writeOpenOrchestrators(path string, entries []orchestratorOpenEntry) error {
	if path == "" {
		return nil
	}
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(orchestratorOpenState{Orchestrators: entries})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
