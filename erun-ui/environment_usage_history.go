package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Persists the usage cache so a restart does not reset every environment to
// "Not yet observed". It is a local, non-authoritative courtesy cache, so it does
// not belong in erun-backend.

const environmentUsageHistoryFileName = "environment-usage-history.json"

// environmentUsageHistoryEntry keys by tenant/environment to keep the file readable.
type environmentUsageHistoryEntry struct {
	Tenant         string         `json:"tenant"`
	Environment    string         `json:"environment"`
	Usage          uiRuntimeUsage `json:"usage"`
	ObservedAtUnix int64          `json:"observedAtUnix"`
}

type environmentUsageHistoryState struct {
	Environments []environmentUsageHistoryEntry `json:"environments,omitempty"`
}

// defaultEnvironmentUsageHistoryPath sits beside orchestrator-nudge-history.json.
func defaultEnvironmentUsageHistoryPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", environmentUsageHistoryFileName)
}

// readEnvironmentUsageHistoryEntries reports unreadable only for an existing,
// unparseable file; a missing file is an ordinary absence.
func readEnvironmentUsageHistoryEntries(path string) (entries []environmentUsageHistoryEntry, unreadable bool) {
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var state environmentUsageHistoryState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("erun-app: environment usage history %s is unreadable: %v", path, err)
		return nil, true
	}
	return state.Environments, false
}

// loadPersistedEnvironmentUsage treats an unreadable file as no readings.
func loadPersistedEnvironmentUsage(path string) map[string]environmentUsageReading {
	entries, unreadable := readEnvironmentUsageHistoryEntries(path)
	if unreadable || len(entries) == 0 {
		return nil
	}
	out := make(map[string]environmentUsageReading, len(entries))
	for _, entry := range entries {
		selection := uimodel.Selection{Tenant: entry.Tenant, Environment: entry.Environment}
		out[selectionKey(selection)] = environmentUsageReading{
			usage:      entry.Usage,
			observedAt: time.Unix(entry.ObservedAtUnix, 0),
		}
	}
	return out
}

// persistEnvironmentUsage logs write failures; the in-memory cache still serves.
func (a *App) persistEnvironmentUsage(selection uimodel.Selection, reading environmentUsageReading) {
	if err := writeEnvironmentUsageHistoryEntry(a.deps.environmentUsageHistoryPath, selection, reading); err != nil {
		log.Printf("erun-app: persist environment usage history %s/%s: %v", selection.Tenant, selection.Environment, err)
	}
}

var errEnvironmentUsageHistoryUnreadable = errors.New("environment usage history file is unreadable")

// writeEnvironmentUsageHistoryEntry upserts atomically and refuses to overwrite
// an unreadable file, which would destroy other environments' entries.
func writeEnvironmentUsageHistoryEntry(path string, selection uimodel.Selection, reading environmentUsageReading) error {
	if path == "" || selection.Tenant == "" || selection.Environment == "" {
		return nil
	}
	entries, unreadable := readEnvironmentUsageHistoryEntries(path)
	if unreadable {
		return errEnvironmentUsageHistoryUnreadable
	}
	out := make([]environmentUsageHistoryEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.Tenant == selection.Tenant && entry.Environment == selection.Environment {
			continue
		}
		out = append(out, entry)
	}
	out = append(out, environmentUsageHistoryEntry{
		Tenant:         selection.Tenant,
		Environment:    selection.Environment,
		Usage:          reading.usage,
		ObservedAtUnix: reading.observedAt.Unix(),
	})
	data, err := json.Marshal(environmentUsageHistoryState{Environments: out})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return config.WriteFileAtomic(path, append(data, '\n'), 0o644)
}
