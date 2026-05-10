package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// activityMarkerPollInterval governs how often the desktop reads the
// running-commands directory. Short enough that a deploy launched in a
// terminal shows up within a single tick; long enough that polling load is
// negligible.
const activityMarkerPollInterval = 1500 * time.Millisecond

// runActivityMarkerWatcher polls the on-disk RunningCommand directory and
// reconciles the in-memory activity queue against it. New markers register
// fresh entries; missing markers (whose entry was loaded from a marker we
// previously saw) finalize the entry. The watcher is the source of truth
// for activity LIFECYCLE — the PTY trace handler only refines status (e.g.
// distinguishing a successful from failed deploy by reading the
// `==> Deploy failed` line on the way out).
func (a *App) runActivityMarkerWatcher(stop <-chan struct{}) {
	dir, err := eruncommon.RunningCommandsDirPath()
	if err != nil || strings.TrimSpace(dir) == "" {
		return
	}
	a.reconcileActivityMarkers(dir)
	ticker := time.NewTicker(activityMarkerPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.reconcileActivityMarkers(dir)
		}
	}
}

// reconcileActivityMarkers reads every marker in dir, registers any that
// the queue does not already track, finalizes entries whose marker has
// recorded a terminal status, prunes markers whose PID is no longer
// alive or whose command type is no longer tracked, and finalizes
// entries whose marker is gone. Errors reading individual markers are
// tolerated — the next tick re-tries.
func (a *App) reconcileActivityMarkers(dir string) {
	if a.activityQueue == nil {
		return
	}
	records, err := eruncommon.ListRunningCommands()
	if err != nil {
		return
	}
	seenIDs := make(map[string]struct{}, len(records))
	for _, record := range records {
		if isOutdatedCLICommand(record.Command) {
			// Markers for command types the current desktop no longer
			// tracks (open, init) are unmistakably from an older `erun`
			// CLI on PATH. Prune them and surface a one-time warning
			// so the user knows to update the CLI.
			a.pruneStaleMarker(dir, record)
			a.notifyCLIOutdatedOnce(record)
			continue
		}
		if record.Status == "" && !isProcessAliveOrDefault(record.PID) {
			// CLI exited without finalizing (crashed, killed via
			// SIGKILL, host shutdown). Clean up so the queue doesn't
			// show a phantom-running entry forever.
			a.pruneStaleMarker(dir, record)
			continue
		}
		entry, fresh := a.applyMarkerRecord(dir, record)
		if entry.ID != "" {
			seenIDs[entry.ID] = struct{}{}
		}
		if fresh {
			a.lockTerminalsForActivity(entry)
			if entry.Command == "deploy" && a.activityStatusPoller != nil {
				a.activityStatusPoller(entry)
			}
		}
		if status := finalStatusFromMarker(record); status != "" {
			if final, ok := a.activityQueue.finish(entry.ID, status, strings.TrimSpace(record.Error)); ok {
				a.unlockTerminalsForActivity(final)
			}
		}
	}
	a.finalizeMissingMarkers(seenIDs)
}

// outdatedCLICommands lists command types that the current desktop no
// longer registers as RunningCommand markers. A marker bearing one of
// these commands must have been written by an older `erun` CLI on PATH;
// the desktop prunes the marker and warns once so the user updates the
// CLI.
var outdatedCLICommands = map[string]struct{}{
	"open": {},
	"init": {},
}

func isOutdatedCLICommand(command string) bool {
	_, ok := outdatedCLICommands[strings.TrimSpace(command)]
	return ok
}

// notifyCLIOutdatedOnce emits a single `activity:cli-outdated` event per
// (command, command-id) pair so the frontend can render a banner. The
// event payload includes the offending command so the toast can suggest
// the right rebuild target.
func (a *App) notifyCLIOutdatedOnce(record eruncommon.RunningCommand) {
	a.cliOutdatedMu.Lock()
	defer a.cliOutdatedMu.Unlock()
	if a.cliOutdatedSeen == nil {
		a.cliOutdatedSeen = make(map[string]struct{})
	}
	key := strings.TrimSpace(record.Command) + "\x00" + strings.TrimSpace(record.ID)
	if _, ok := a.cliOutdatedSeen[key]; ok {
		return
	}
	a.cliOutdatedSeen[key] = struct{}{}
	a.emitEvent(activityCLIOutdatedEvent, cliOutdatedPayload{
		Command:  strings.TrimSpace(record.Command),
		Tenant:   strings.TrimSpace(record.Tenant),
		PID:      record.PID,
		Reason:   "Activity marker written by an older `erun` CLI on PATH; rebuild and reinstall the CLI to clear this warning.",
		Suggest:  "make install / go install ./erun-cli/...",
	})
}

const activityCLIOutdatedEvent = "activity:cli-outdated"

type cliOutdatedPayload struct {
	Command string `json:"command"`
	Tenant  string `json:"tenant,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Reason  string `json:"reason"`
	Suggest string `json:"suggest,omitempty"`
}

// pruneStaleMarker removes the on-disk marker for a process that is no
// longer alive. If the marker's record matches an active queue entry the
// entry is finalized as "failed" with a clear reason so the user sees the
// abandoned activity rather than nothing.
func (a *App) pruneStaleMarker(dir string, record eruncommon.RunningCommand) {
	id := strings.TrimSpace(record.ID)
	if id == "" {
		return
	}
	path := filepath.Join(dir, sanitizeFilenameForActivity(id)+".json")
	_ = os.Remove(path)
	if a.activityQueue == nil {
		return
	}
	if final, ok := a.activityQueue.finish(id, activityQueueStatusFailed, "command exited without recording a terminal status (likely killed)"); ok {
		a.unlockTerminalsForActivity(final)
	}
}

// isProcessAliveOrDefault returns true when the supplied PID is currently
// running, with a permissive fallback when the PID is unset or invalid
// (which happens for older markers written before PID was recorded).
func isProcessAliveOrDefault(pid int) bool {
	if pid <= 0 {
		return true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	signalErr := proc.Signal(syscall.Signal(0))
	if signalErr == nil {
		return true
	}
	if errors.Is(signalErr, syscall.ESRCH) {
		return false
	}
	return true
}

// finalStatusFromMarker translates the marker's Status field (set by
// FinalizeRunningCommand) into the desktop's queue status enum.
func finalStatusFromMarker(record eruncommon.RunningCommand) activityQueueStatus {
	switch strings.TrimSpace(record.Status) {
	case "succeeded":
		return activityQueueStatusSucceeded
	case "failed":
		return activityQueueStatusFailed
	case "skipped":
		return activityQueueStatusSkipped
	default:
		return ""
	}
}

func (a *App) applyMarkerRecord(dir string, record eruncommon.RunningCommand) (activityQueueEntry, bool) {
	if a.activityQueue == nil {
		return activityQueueEntry{}, false
	}
	id := strings.TrimSpace(record.ID)
	if id == "" {
		return activityQueueEntry{}, false
	}
	markerPath := filepath.Join(dir, sanitizeFilenameForActivity(id)+".json")
	seed := activityQueueEntry{
		ID:                id,
		Command:           strings.TrimSpace(record.Command),
		Tenant:            strings.TrimSpace(record.Tenant),
		Environment:       strings.TrimSpace(record.Environment),
		Version:           strings.TrimSpace(record.Version),
		Release:           strings.TrimSpace(record.Release),
		Namespace:         strings.TrimSpace(record.Namespace),
		KubernetesContext: strings.TrimSpace(record.KubernetesContext),
		Component:         strings.TrimSpace(record.Component),
		Image:             strings.TrimSpace(record.Image),
		Summary:           strings.TrimSpace(record.Summary),
		StartedAt:         record.StartedAt,
		MarkerPath:        markerPath,
	}
	return a.activityQueue.start(seed)
}

// finalizeMissingMarkers walks the active set and finishes entries whose
// MarkerPath is set but whose marker is no longer in the seen set this
// tick. Entries without a MarkerPath were created in-memory and are
// finalized through the trace handler instead.
func (a *App) finalizeMissingMarkers(seenIDs map[string]struct{}) {
	if a.activityQueue == nil {
		return
	}
	a.activityQueue.mu.Lock()
	var stale []activityQueueEntry
	for id, entry := range a.activityQueue.active {
		if entry.MarkerPath == "" {
			continue
		}
		if _, ok := seenIDs[id]; ok {
			continue
		}
		stale = append(stale, *cloneActivityQueueEntry(entry))
	}
	a.activityQueue.mu.Unlock()
	for _, entry := range stale {
		// Status fallback: if the trace handler already saw a Deploy
		// failed line we'd have transitioned via finishActivityTracking
		// before reaching here. Marker disappearance with no failed-line
		// observed by this desktop is treated as success.
		status := activityQueueStatusSucceeded
		if final, ok := a.activityQueue.finish(entry.ID, status, ""); ok {
			a.unlockTerminalsForActivity(final)
		}
	}
}

// stopActivityMarkerWatcher cancels the background watcher goroutine if it
// was started. Called from App.shutdown.
func (a *App) stopActivityMarkerWatcher() {
	if a.activityWatcherStop == nil {
		return
	}
	close(a.activityWatcherStop)
	a.activityWatcherStop = nil
}

// activityWatcherCtx is a small helper that returns a cancellable context
// derived from the app context if one is available, or context.Background
// otherwise. Used by the watcher when it spawns ad-hoc kubectl polls.
func (a *App) activityWatcherCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func sanitizeFilenameForActivity(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}
