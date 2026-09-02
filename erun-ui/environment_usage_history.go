package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// environment_usage.go's cache (a.envUsage) used to live only in memory, so a
// desktop restart reset every environment to "Not yet observed" even though
// the hover card had shown real figures moments before the process exited.
// Unlike orchestrator_nudge_history.go's cumulative counters, a lost usage
// reading is not unrecoverable -- the next sweep tick takes a fresh, equally
// true sample -- but with one kubectl-exec probe per configured environment
// on a 90s ticker (environment_usage.go), the gap before that first sweep
// completes routinely spans minutes, and it recurs on every rebuild-and-relaunch
// of the desktop: the normal development loop. This file is the durable half,
// following orchestrator_nudge_history.go's shape: one small JSON file, keyed
// by environment, written on every sample and read back once at startup to
// seed a.envUsage before the first hover card ever renders.
//
// This stays a local desktop file rather than a row in erun-backend's
// Postgres: it is a courtesy cache of what this one desktop last observed by
// shelling into a pod, superseded by a fresh sample within one sweep interval,
// never authoritative, never shared across machines or tenants, and never
// read by anything other than this process. None of the reasons erun-backend
// owns durable multi-tenant state (shared visibility, authority, migrations)
// apply to it, and orchestrator-nudge-history.json already set this precedent
// for the identically-shaped problem on the same card.

const environmentUsageHistoryFileName = "environment-usage-history.json"

// environmentUsageHistoryEntry is one environment's last cached usage
// reading, keyed by tenant/environment name rather than selectionKey's joined
// form so the file stays human-readable.
type environmentUsageHistoryEntry struct {
	Tenant         string         `json:"tenant"`
	Environment    string         `json:"environment"`
	Usage          uiRuntimeUsage `json:"usage"`
	ObservedAtUnix int64          `json:"observedAtUnix"`
}

type environmentUsageHistoryState struct {
	Environments []environmentUsageHistoryEntry `json:"environments,omitempty"`
}

// defaultEnvironmentUsageHistoryPath is a sibling of orchestrator-nudge-history.json
// under UserConfigDir()/ERun.
func defaultEnvironmentUsageHistoryPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", environmentUsageHistoryFileName)
}

// readEnvironmentUsageHistoryEntries returns every persisted entry.
// unreadable is true only when the file exists but its content could not be
// parsed -- a missing file is a real, ordinary absence (nothing has ever been
// sampled, or nothing has ever been persisted yet) and reports no entries
// with unreadable=false, never the reverse.
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

// loadPersistedEnvironmentUsage seeds a.envUsage at startup. An unreadable
// file is logged and otherwise treated as no persisted readings at all: every
// environment falls back to the pre-existing "Not yet observed" state rather
// than fabricating a stale figure from data that could not actually be read.
func loadPersistedEnvironmentUsage(path string) map[string]environmentUsageReading {
	entries, unreadable := readEnvironmentUsageHistoryEntries(path)
	if unreadable || len(entries) == 0 {
		return nil
	}
	out := make(map[string]environmentUsageReading, len(entries))
	for _, entry := range entries {
		selection := uiSelection{Tenant: entry.Tenant, Environment: entry.Environment}
		out[selectionKey(selection)] = environmentUsageReading{
			usage:      entry.Usage,
			observedAt: time.Unix(entry.ObservedAtUnix, 0),
		}
	}
	return out
}

// persistEnvironmentUsage writes selection's reading to disk, logging rather
// than failing the caller: a reading that could not be persisted is still
// cached in memory and still rendered this session, so a write failure in the
// durable half must not undo the in-memory half sampleEnvironmentUsageOnce
// already updated.
func (a *App) persistEnvironmentUsage(selection uiSelection, reading environmentUsageReading) {
	if err := writeEnvironmentUsageHistoryEntry(a.deps.environmentUsageHistoryPath, selection, reading); err != nil {
		log.Printf("erun-app: persist environment usage history %s/%s: %v", selection.Tenant, selection.Environment, err)
	}
}

// errEnvironmentUsageHistoryUnreadable is returned by the write helper when
// the existing file cannot be parsed, so a caller can log the refusal to
// write rather than treat it as a silent no-op.
var errEnvironmentUsageHistoryUnreadable = errors.New("environment usage history file is unreadable")

// writeEnvironmentUsageHistoryEntry upserts one environment's record and
// persists the full set atomically (temp file + rename, the same pattern
// erun-common's config writers use) so a crash mid-write never leaves this
// file half-written for every OTHER environment's history along with this
// one's.
//
// If the existing file is unreadable, this refuses to write rather than
// silently replacing every other environment's history with a set containing
// only this one entry: better to miss one update (the in-memory cache keeps
// serving this process regardless) than to destroy the rest.
func writeEnvironmentUsageHistoryEntry(path string, selection uiSelection, reading environmentUsageReading) error {
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
	return eruncommon.WriteFileAtomic(path, append(data, '\n'), 0o644)
}
