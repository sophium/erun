package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The open set is written on start and cleared on stop, never at shutdown, since
// a crash or reboot runs no hook. It is kept in recency order: app_restart.go
// gives the pane to the most recently (re)started orchestrator.

const orchestratorOpenFileName = "orchestrator-open.json"

type OpenEntry struct {
	OrchestratorID string `json:"orchestratorId"`
	// AttachedConversationID is legacy: never set, only carried until the agent resumes it.
	AttachedConversationID string `json:"attachedConversationId,omitempty"`
}

type orchestratorOpenState struct {
	// Orchestrators is oldest first.
	Orchestrators []OpenEntry `json:"orchestrators,omitempty"`
	// OrchestratorIDs is a legacy shape: read, never written.
	OrchestratorIDs []string `json:"orchestratorIds,omitempty"`
	// OrchestratorID is an older legacy shape: read, never written.
	OrchestratorID string `json:"orchestratorId,omitempty"`
}

func DefaultOpenPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", orchestratorOpenFileName)
}

// recordOpenOrchestrator adds or moves an orchestrator to the most-recent end.
func recordOpenOrchestrator(path, orchestratorID string) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	entries := ReadOpenOrchestrators(path)
	out := make([]OpenEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.OrchestratorID == orchestratorID {
			continue
		}
		out = append(out, entry)
	}
	// The agent exists by now and already resumed any legacy attached conversation.
	out = append(out, OpenEntry{OrchestratorID: orchestratorID})
	return writeOpenOrchestrators(path, out)
}

// clearOpenOrchestrator forgets one orchestrator, leaving the rest open.
func clearOpenOrchestrator(path, orchestratorID string) error {
	orchestratorID = strings.TrimSpace(orchestratorID)
	if path == "" || orchestratorID == "" {
		return nil
	}
	entries := ReadOpenOrchestrators(path)
	out := make([]OpenEntry, 0, len(entries))
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

// ReadOpenOrchestrators returns the open set oldest first without clearing it.
func ReadOpenOrchestrators(path string) []OpenEntry {
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
	legacy := make([]OpenEntry, 0, len(state.OrchestratorIDs)+1)
	if id := strings.TrimSpace(state.OrchestratorID); id != "" {
		legacy = append(legacy, OpenEntry{OrchestratorID: id})
	}
	for _, id := range state.OrchestratorIDs {
		legacy = append(legacy, OpenEntry{OrchestratorID: id})
	}
	return dedupOrchestratorEntries(legacy)
}

func dedupOrchestratorEntries(entries []OpenEntry) []OpenEntry {
	seen := make(map[string]struct{}, len(entries))
	out := make([]OpenEntry, 0, len(entries))
	for _, entry := range entries {
		id := strings.TrimSpace(entry.OrchestratorID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, OpenEntry{
			OrchestratorID:         id,
			AttachedConversationID: strings.TrimSpace(entry.AttachedConversationID),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// writeOpenOrchestrators migrates legacy shapes and removes the file when empty.
func writeOpenOrchestrators(path string, entries []OpenEntry) error {
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
