package orchestrator

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

// errOrchestratorNudgeHistoryUnreadable is returned when the existing file cannot be parsed.
var errOrchestratorNudgeHistoryUnreadable = errors.New("orchestrator nudge history file is unreadable")

// Cumulative pacing history survives a desktop restart because the session and
// its pacer carry on regardless. Ids are name-derived and reused, so
// DeleteOrchestrator clears the record; stop/start keeps it.

const orchestratorNudgeHistoryFileName = "orchestrator-nudge-history.json"

// orchestratorNudgeHistoryEntry holds only the fields that never reset on rearm.
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

func DefaultNudgeHistoryPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", orchestratorNudgeHistoryFileName)
}

// readOrchestratorNudgeHistoryEntries returns entries oldest first; unreadable
// is true only when the file exists but cannot be parsed.
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

// orchestratorNudgeHistoryFor reads the file and looks up one record.
func orchestratorNudgeHistoryFor(path, orchestratorID string) (entry orchestratorNudgeHistoryEntry, found, unreadable bool) {
	entries, unreadable := readOrchestratorNudgeHistoryEntries(path)
	if unreadable {
		return orchestratorNudgeHistoryEntry{}, false, true
	}
	entry, found = orchestratorNudgeHistoryEntryIn(entries, orchestratorID)
	return entry, found, false
}

// orchestratorNudgeHistoryEntryIn looks up one record in already-read entries.
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

// restoreOrchestratorNudgeHistory seeds a (re)spawned session from its persisted
// record; transient sessions are skipped and an unreadable file marks the session.
func (s *Service) restoreOrchestratorNudgeHistory(session *orchestratorSession) {
	if session.transient {
		return
	}
	history, found, unreadable := orchestratorNudgeHistoryFor(s.d.NudgeHistoryPath(), session.id)
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

// orchestratorNudgeHistoryEntryFromSession builds the entry to persist; persist
// is false for a transient session, whose id is never reused.
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

// persistOrchestratorNudgeHistory logs rather than fails: the nudge already landed.
func (s *Service) persistOrchestratorNudgeHistory(entry orchestratorNudgeHistoryEntry, persist bool) {
	if !persist {
		return
	}
	if err := writeOrchestratorNudgeHistoryEntry(s.d.NudgeHistoryPath(), entry); err != nil {
		log.Printf("erun-app: persist orchestrator nudge history %s: %v", entry.OrchestratorID, err)
	}
}

// writeOrchestratorNudgeHistoryEntry upserts atomically and refuses to write over
// an unreadable file rather than destroy every other orchestrator's history.
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

// clearOrchestratorNudgeHistoryEntry removes one record so a reused id starts fresh.
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
	return config.WriteFileAtomic(path, append(data, '\n'), 0o644)
}
