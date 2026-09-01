package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// errOrchestratorNudgeHistoryUnreadable is returned by the write helpers when
// the existing file cannot be parsed, so a caller can log the refusal to
// write rather than treat it as a silent no-op.
var errOrchestratorNudgeHistoryUnreadable = errors.New("orchestrator nudge history file is unreadable")

// The cumulative pacing history (orchestrator.go's pacingAutoNudgeCount /
// pacingWhipCount / pacingLastCappedAtUnix and their timestamps) used to live
// only on the in-memory orchestratorSession, so a desktop restart reset it to
// zero while the orchestrator's terminal session -- and the pacer nudging it
// -- carried on unaffected. The hover card then read "Not nudged" for a
// session that had been nudged continuously, exactly the ambiguity #1758
// removed from the live cap counters. This file is the durable half: written
// on every nudge and read back whenever a session for that orchestrator id is
// (re)spawned, so the record outlives the process the same way the pacing it
// describes does.
//
// It is keyed by orchestrator id and kept as one file, the same shape as
// orchestrator-open.json (orchestrator_open_state.go), for the same reason:
// one small read/write per poll rather than one file per orchestrator.
//
// An orchestrator id is a name-derived slug (uniqueOrchestratorID), not a
// uuid, so deleting an orchestrator and later creating a new one with the
// same name reuses it. DeleteOrchestrator clears this record when it removes
// the definition, so a reused id starts "never nudged" again rather than
// inheriting a stranger's history. Stopping and restarting the same
// orchestrator is not a delete: its record is left alone and restored on the
// next spawn, which is the "reattach" case the persistence exists for.

const orchestratorNudgeHistoryFileName = "orchestrator-nudge-history.json"

// orchestratorNudgeHistoryEntry is one orchestrator's cumulative pacing
// record. It carries only the fields that never reset on rearm — the live
// cap gauge (pacingNudgeCount/pacingCapped/pacingLastNudgeAtUnix) is not
// here, because it is meaningless once nothing is running.
type orchestratorNudgeHistoryEntry struct {
	OrchestratorID      string `json:"orchestratorId"`
	AutoNudgeCount      int    `json:"autoNudgeCount,omitempty"`
	LastAutoNudgeAtUnix int64  `json:"lastAutoNudgeAtUnix,omitempty"`
	WhipCount           int    `json:"whipCount,omitempty"`
	LastWhipAtUnix      int64  `json:"lastWhipAtUnix,omitempty"`
	LastCappedAtUnix    int64  `json:"lastCappedAtUnix,omitempty"`
}

type orchestratorNudgeHistoryState struct {
	Orchestrators []orchestratorNudgeHistoryEntry `json:"orchestrators,omitempty"`
}

// defaultOrchestratorNudgeHistoryPath is a sibling of orchestrator-open.json
// under UserConfigDir()/ERun.
func defaultOrchestratorNudgeHistoryPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", orchestratorNudgeHistoryFileName)
}

// readOrchestratorNudgeHistoryEntries returns every persisted entry, oldest
// first. unreadable is true only when the file exists but its content could
// not be parsed -- a missing file is a real, ordinary absence (nothing has
// ever been nudged, or nothing has ever been persisted yet) and reports no
// entries with unreadable=false, never the reverse.
func readOrchestratorNudgeHistoryEntries(path string) (entries []orchestratorNudgeHistoryEntry, unreadable bool) {
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var state orchestratorNudgeHistoryState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("erun-app: orchestrator nudge history %s is unreadable: %v", path, err)
		return nil, true
	}
	return state.Orchestrators, false
}

// orchestratorNudgeHistoryFor looks up one orchestrator's persisted record,
// reading the file itself. Prefer orchestratorNudgeHistoryEntryIn when the
// caller already has the entries in hand (e.g. ListOrchestrators, which reads
// the file once for every orchestrator it lists rather than once per one).
func orchestratorNudgeHistoryFor(path, orchestratorID string) (entry orchestratorNudgeHistoryEntry, found, unreadable bool) {
	entries, unreadable := readOrchestratorNudgeHistoryEntries(path)
	if unreadable {
		return orchestratorNudgeHistoryEntry{}, false, true
	}
	entry, found = orchestratorNudgeHistoryEntryIn(entries, orchestratorID)
	return entry, found, false
}

// orchestratorNudgeHistoryEntryIn looks up one orchestrator's record within
// an already-read entry set. found is false when there is no entry for id --
// a clean "never nudged, as far as we know" -- which the caller distinguishes
// from "unreadable" using the flag readOrchestratorNudgeHistoryEntries
// returned alongside these entries.
func orchestratorNudgeHistoryEntryIn(entries []orchestratorNudgeHistoryEntry, orchestratorID string) (entry orchestratorNudgeHistoryEntry, found bool) {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if orchestratorID == "" {
		return orchestratorNudgeHistoryEntry{}, false
	}
	for _, e := range entries {
		if e.OrchestratorID == orchestratorID {
			return e, true
		}
	}
	return orchestratorNudgeHistoryEntry{}, false
}

// restoreOrchestratorNudgeHistory seeds a freshly constructed session's
// cumulative pacing fields from the persisted record for its id, called from
// spawnOrchestratorSession on every (re)spawn -- the "reattach" this issue's
// persistence exists for. A transient (Investigate) session is skipped, the
// same as orchestratorNudgeHistoryEntryFromSession's write side: it has no
// persisted definition to reattach to. An unreadable file marks the session
// rather than silently leaving it at a confident zero.
func (a *App) restoreOrchestratorNudgeHistory(session *orchestratorSession) {
	if session.transient {
		return
	}
	history, found, unreadable := orchestratorNudgeHistoryFor(a.deps.orchestratorNudgeHistoryPath, session.id)
	if unreadable {
		session.pacingHistoryUnreadable = true
		return
	}
	if !found {
		return
	}
	session.pacingAutoNudgeCount = history.AutoNudgeCount
	session.pacingLastAutoNudgeAtUnix = history.LastAutoNudgeAtUnix
	session.pacingWhipCount = history.WhipCount
	session.pacingLastWhipAtUnix = history.LastWhipAtUnix
	session.pacingLastCappedAtUnix = history.LastCappedAtUnix
}

// orchestratorNudgeHistoryEntryFromSession builds the persisted-history entry
// for a session's current cumulative pacing counters. persist is false for a
// transient (Investigate) session: it has no persisted definition and its id
// is minted fresh per investigation, so it has nothing to reattach a record
// to and would only leave a one-off entry behind forever.
func orchestratorNudgeHistoryEntryFromSession(session *orchestratorSession) (entry orchestratorNudgeHistoryEntry, persist bool) {
	if session.transient {
		return orchestratorNudgeHistoryEntry{}, false
	}
	return orchestratorNudgeHistoryEntry{
		OrchestratorID:      session.id,
		AutoNudgeCount:      session.pacingAutoNudgeCount,
		LastAutoNudgeAtUnix: session.pacingLastAutoNudgeAtUnix,
		WhipCount:           session.pacingWhipCount,
		LastWhipAtUnix:      session.pacingLastWhipAtUnix,
		LastCappedAtUnix:    session.pacingLastCappedAtUnix,
	}, true
}

// persistOrchestratorNudgeHistory writes an entry built by
// orchestratorNudgeHistoryEntryFromSession, logging rather than failing the
// caller: a nudge that could not be persisted still landed in the pane and
// still counted against the live cap, so the operator-visible half of the
// nudge must not be undone by a write failure in the durable half.
func (a *App) persistOrchestratorNudgeHistory(entry orchestratorNudgeHistoryEntry, persist bool) {
	if !persist {
		return
	}
	if err := writeOrchestratorNudgeHistoryEntry(a.deps.orchestratorNudgeHistoryPath, entry); err != nil {
		log.Printf("erun-app: persist orchestrator nudge history %s: %v", entry.OrchestratorID, err)
	}
}

// writeOrchestratorNudgeHistoryEntry upserts one orchestrator's record and
// persists the full set atomically (temp file + rename, the same pattern
// erun-common's config writers use for exactly this reason -- see
// eruncommon.WriteFileAtomic) so a crash mid-write never leaves this file
// half-written for every OTHER orchestrator's history along with this one's.
//
// If the existing file is unreadable, this refuses to write rather than
// silently replacing every other orchestrator's history with a set
// containing only this one entry: better to miss one update (the live
// session keeps counting in memory regardless) than to destroy the rest.
func writeOrchestratorNudgeHistoryEntry(path string, entry orchestratorNudgeHistoryEntry) error {
	entry.OrchestratorID = strings.TrimSpace(entry.OrchestratorID)
	if path == "" || entry.OrchestratorID == "" {
		return nil
	}
	entries, unreadable := readOrchestratorNudgeHistoryEntries(path)
	if unreadable {
		return errOrchestratorNudgeHistoryUnreadable
	}
	out := make([]orchestratorNudgeHistoryEntry, 0, len(entries)+1)
	for _, e := range entries {
		if e.OrchestratorID == entry.OrchestratorID {
			continue
		}
		out = append(out, e)
	}
	out = append(out, entry)
	return writeOrchestratorNudgeHistoryEntries(path, out)
}

// clearOrchestratorNudgeHistoryEntry removes one orchestrator's record, used
// when its definition is deleted so a later orchestrator that reuses the same
// name-derived id starts "never nudged" rather than inheriting history that
// belonged to a different, deleted orchestrator.
func clearOrchestratorNudgeHistoryEntry(path, orchestratorID string) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	entries, unreadable := readOrchestratorNudgeHistoryEntries(path)
	if unreadable {
		return errOrchestratorNudgeHistoryUnreadable
	}
	out := make([]orchestratorNudgeHistoryEntry, 0, len(entries))
	removed := false
	for _, e := range entries {
		if e.OrchestratorID == orchestratorID {
			removed = true
			continue
		}
		out = append(out, e)
	}
	if !removed {
		return nil
	}
	return writeOrchestratorNudgeHistoryEntries(path, out)
}

func writeOrchestratorNudgeHistoryEntries(path string, entries []orchestratorNudgeHistoryEntry) error {
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(orchestratorNudgeHistoryState{Orchestrators: entries})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return eruncommon.WriteFileAtomic(path, append(data, '\n'), 0o644)
}
