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
// This file owns the DURABLE one: which orchestrators are open, and which
// conversation each was last known to be running. It is written the moment a
// session starts and cleared the moment the operator stops it — never at
// shutdown, because a crash, a `pkill` or a reboot takes the desktop away
// without running any hook, and a record only a clean quit could write would be
// missing exactly when it is most needed. It carries no timestamp either: an
// orchestrator the operator left open is still the one they were in, however
// long ago that was.
//
// The record is a SET, not a single id: every orchestrator the operator had
// open comes back, not just the last one started. It is kept in recency order —
// each (re)start moves its entry to the end — because that order is also how
// app_restart.go picks which one owns the terminal pane when no restart
// hand-off names one: the most recently (re)started orchestrator, on the theory
// that starting one is also how the operator ends up looking at it.
//
// Each entry carries the conversation id it was actually spawned with, not just
// the orchestrator id. A restore that only had the orchestrator id used to
// re-derive a session id from it (uuid5(namespace, id)) and resume THAT — which
// is only correct while the live session's id is always the derived one. It is
// not: a resume that falls through to an unpinned launch, or a corrupted
// transcript, can leave the live conversation at a different id than the one
// derived from the orchestrator's own. Recording the conversation id at spawn
// time (see recordOpenOrchestrator's caller) is what lets a restore resume the
// conversation that was actually there instead of guessing.
//
// app_restart.go owns the OTHER record: the one-shot hand-off an in-app restart
// writes, which carries the prompt the resumed session should auto-run. That one
// stays one-shot and age-bounded, so a rebuild+restart continues its task while
// a plain launch resumes the conversation idle at the prompt.

const orchestratorOpenFileName = "orchestrator-open.json"

// orchestratorOpenEntry is one orchestrator in the durable open set: its id and
// the conversation last known to be running under it. SessionID is empty for an
// entry migrated from a release that predates this file (see
// orchestratorOpenState), which restore treats as "no live session recorded"
// rather than a session pinned to nothing.
type orchestratorOpenEntry struct {
	OrchestratorID string `json:"orchestratorId"`
	SessionID      string `json:"sessionId,omitempty"`
	// Environments is the scope (sorted tenant/environment pairs, see
	// orchestratorScopeOf) the recorded session was actually wired to when this
	// entry was written. An orchestrator id is mutable and reusable, so restore
	// compares this against the orchestrator's CURRENT scope: a re-scoped id must
	// not silently resume a conversation carrying context for environments it no
	// longer has, with nothing said about it. Empty for an entry a pre-scope-aware
	// release wrote, which restore treats as unknown rather than a guaranteed
	// match.
	Environments []string `json:"environments,omitempty"`
}

type orchestratorOpenState struct {
	// Orchestrators is the set of orchestrators open when the desktop was last
	// running, oldest first, each carrying the conversation id it was actually
	// spawned with.
	Orchestrators []orchestratorOpenEntry `json:"orchestrators,omitempty"`
	// OrchestratorIDs is the shape this file had under an earlier release,
	// before an entry carried a session id at all. Only read, never written
	// again: an operator upgrading from that release must not lose the
	// orchestrators they had open — they come back with no recorded session,
	// so restore starts each of them on a fresh conversation rather than
	// guessing which one was theirs.
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

// recordOpenOrchestrator adds an orchestrator to the open set together with the
// conversation id it was just spawned with, or moves it to the end (most
// recent) with that conversation id if it was already there. Called every time
// a session is spawned, so the record always names the conversation this
// launch is actually running rather than one a restore would have to derive.
func recordOpenOrchestrator(path, orchestratorID, sessionID string, scope []string) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	entries := readOpenOrchestrators(path)
	out := make([]orchestratorOpenEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.OrchestratorID == orchestratorID {
			continue
		}
		// A conversation belongs to one orchestrator. If another entry already
		// names the session this launch is running, it is a stale claim by
		// definition — this launch is the one with the conversation open now —
		// so release it rather than leaving the id under two owners, which is
		// what made a crossing stick across every later restart.
		if sessionID != "" && strings.TrimSpace(entry.SessionID) == sessionID {
			entry.SessionID = ""
		}
		out = append(out, entry)
	}
	out = append(out, orchestratorOpenEntry{OrchestratorID: orchestratorID, SessionID: sessionID, Environments: scope})
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
// desktop last ran, oldest first, each with the conversation id it was last
// known to be running (empty when a restore must not assume one — see
// orchestratorOpenState). Reading does not clear the record: it is durable, so
// every launch reopens the same set until an entry is stopped.
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
	// A legacy file (the older scalar shape, or the later id-only set) never
	// recorded a session id, so every id it names comes back with none —
	// restore must start each of them fresh rather than resolving to whatever
	// its derived id happens to already name on disk.
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
		out = append(out, orchestratorOpenEntry{OrchestratorID: id, SessionID: strings.TrimSpace(entry.SessionID), Environments: entry.Environments})
	}
	if len(out) == 0 {
		return nil
	}
	return dropDuplicateSessionClaims(out)
}

// dropDuplicateSessionClaims enforces the invariant the rest of restore assumes
// but nothing used to check: a conversation belongs to exactly ONE orchestrator.
//
// Deduping by orchestrator id alone let one session id sit under two ids at
// once, and every later launch then resolved both of them to the same
// conversation — so one orchestrator was handed the other's history, complete
// with the other's scope and return note, and the crossing was self-reinforcing
// because each launch recorded it again.
//
// Entries are oldest-first, so the walk runs backwards and the MOST RECENT claim
// on a session keeps it. An older entry that named the same conversation stays
// open — the operator had it open, and closing it would lose more than it fixes
// — but comes back with no session id, which restore already treats as "start
// this one fresh" rather than guessing.
func dropDuplicateSessionClaims(entries []orchestratorOpenEntry) []orchestratorOpenEntry {
	claimed := make(map[string]string, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		session := strings.TrimSpace(entries[i].SessionID)
		if session == "" {
			continue
		}
		if owner, ok := claimed[session]; ok && owner != entries[i].OrchestratorID {
			entries[i].SessionID = ""
			continue
		}
		claimed[session] = entries[i].OrchestratorID
	}
	return entries
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
