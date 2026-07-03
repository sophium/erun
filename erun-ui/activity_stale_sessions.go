package main

import (
	"strconv"
	"strings"
	"time"
)

// staleShellDebounce keeps a freshly-started PTY session out of the
// stale-detector's reach for the first interval. Some shells fork their
// children before the parent exits, and we don't want a transient
// not-running window to surface a false-positive in the activity drawer.
const staleShellDebounce = 4 * activityPollerInterval

// runStaleShellDetector reconciles open PTY sessions with the OS so
// shells that died without closing their pty (signalled externally,
// kernel OOM, kubectl exec lost) become visible in the activity queue
// with a kill option. Healthy shells stay invisible — the queue is for
// items that need user attention.
func (a *App) runStaleShellDetector(stop <-chan struct{}) {
	ticker := time.NewTicker(activityPollerInterval)
	defer ticker.Stop()
	a.reconcileStaleShellsOnce()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.reconcileStaleShellsOnce()
		}
	}
}

func (a *App) reconcileStaleShellsOnce() {
	if a.activityQueue == nil {
		return
	}
	stale := a.collectStaleSessionSnapshots()
	seenIDs := make(map[string]struct{}, len(stale))
	for _, snapshot := range stale {
		entry, _ := a.upsertStaleShellActivity(snapshot)
		if entry.ID != "" {
			seenIDs[entry.ID] = struct{}{}
		}
	}
	a.removeRecoveredStaleShellActivities(seenIDs)
}

type staleSessionSnapshot struct {
	serial    int
	kind      sessionKind
	selection uiSelection
	startedAt time.Time
	pid       int
}

// Snapshots stale sessions under a brief App-mutex hold so the surrounding
// reconciler stays unlocked and can't deadlock kubectl/helm subprocesses
// against terminal startup.
func (a *App) collectStaleSessionSnapshots() []staleSessionSnapshot {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []staleSessionSnapshot
	for _, managed := range a.sessions {
		if managed == nil || managed.closed || managed.session == nil {
			continue
		}
		if managed.startedAt.IsZero() || now.Sub(managed.startedAt) < staleShellDebounce {
			continue
		}
		pid := managed.session.Pid()
		if pid <= 0 {
			continue
		}
		if isProcessAliveOrDefault(pid) {
			continue
		}
		out = append(out, staleSessionSnapshot{
			serial:    managed.serial,
			kind:      managed.kind,
			selection: managed.selection,
			startedAt: managed.startedAt,
			pid:       pid,
		})
	}
	return out
}

func (a *App) upsertStaleShellActivity(snapshot staleSessionSnapshot) (activityQueueEntry, bool) {
	id := staleShellActivityID(snapshot.serial)
	tenant := strings.TrimSpace(snapshot.selection.Tenant)
	environment := strings.TrimSpace(snapshot.selection.Environment)
	command := staleShellCommandFromKind(snapshot.kind)
	summary := staleShellSummary(command, tenant, environment)
	return a.activityQueue.start(activityQueueEntry{
		ID:                id,
		Command:           command,
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: strings.TrimSpace(snapshot.selection.KubernetesContext),
		Summary:           summary,
		Source:            "shell",
		SessionID:         strconv.Itoa(snapshot.serial),
		StartedAt:         snapshot.startedAt.UTC(),
		Error:             "shell exited unexpectedly (use Kill to dismiss)",
	})
}

func (a *App) removeRecoveredStaleShellActivities(stillStale map[string]struct{}) {
	if a.activityQueue == nil {
		return
	}
	for _, entry := range a.activityQueue.list() {
		if entry.Source != "shell" {
			continue
		}
		if entry.Status != activityQueueStatusRunning {
			continue
		}
		if _, ok := stillStale[entry.ID]; ok {
			continue
		}
		a.activityQueue.forceDismiss(entry.ID)
		a.emitActivityState(entry)
	}
}

// KillSession backs the activity drawer's Kill button: it terminates a stale
// session and dismisses its queue entry.
func (a *App) KillSession(serial int) bool {
	if serial <= 0 {
		return false
	}
	a.mu.Lock()
	var managed *managedTerminal
	for _, candidate := range a.sessions {
		if candidate != nil && candidate.serial == serial {
			managed = candidate
			break
		}
	}
	a.mu.Unlock()
	if managed == nil {
		return false
	}
	if managed.session != nil {
		_ = managed.session.Close()
	}
	id := staleShellActivityID(serial)
	if a.activityQueue != nil {
		if entry, _, ok := a.activityQueue.forceDismiss(id); ok {
			a.unlockTerminalsForActivity(entry)
			a.emitActivityState(entry)
		}
	}
	return true
}

func staleShellActivityID(serial int) string {
	return "shell:" + strconv.Itoa(serial)
}

func staleShellCommandFromKind(kind sessionKind) string {
	switch kind {
	case sessionKindOpen:
		return "open"
	case sessionKindAI:
		return "ai"
	case sessionKindLocal:
		return "local"
	case sessionKindCommand:
		return "command"
	default:
		return string(kind)
	}
}

func staleShellSummary(command, tenant, environment string) string {
	parts := []string{command}
	if tenant != "" || environment != "" {
		target := tenant
		if environment != "" {
			if target != "" {
				target += "/"
			}
			target += environment
		}
		parts = append(parts, target)
	}
	return strings.Join(parts, " ")
}
