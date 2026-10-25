package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// staleShellDebounce skips fresh sessions, since some shells fork children before the parent exits.
const staleShellDebounce = 4 * activityPollerInterval

// runStaleShellDetector surfaces shells that died without closing their PTY.
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
	kind      terminal.Kind
	selection uimodel.Selection
	startedAt time.Time
	pid       int
}

// The App mutex is held only briefly so the reconciler cannot deadlock against terminal startup.
func (a *App) collectStaleSessionSnapshots() []staleSessionSnapshot {
	now := time.Now()
	var out []staleSessionSnapshot
	a.terminal.WithLock(func(l *terminal.Locked) {
		for _, managed := range l.Sessions() {
			if managed == nil || managed.Closed || managed.PTY == nil {
				continue
			}
			if managed.StartedAt.IsZero() || now.Sub(managed.StartedAt) < staleShellDebounce {
				continue
			}
			pid := managed.PTY.Pid()
			if pid <= 0 {
				continue
			}
			// Ask the session, not the PID: EDR on locked-down Windows denies process probes.
			if managed.PTY.Alive() {
				continue
			}
			out = append(out, staleSessionSnapshot{
				serial:    managed.Serial,
				kind:      managed.Kind,
				selection: managed.Selection,
				startedAt: managed.StartedAt,
				pid:       pid,
			})
		}
	})
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

// KillSession terminates a stale session and dismisses its entry. It marks the
// session closed first so the detector does not re-surface the reaped process.
func (a *App) KillSession(serial int) bool {
	if serial <= 0 {
		return false
	}
	var managed *terminal.Session
	var session terminal.PTY
	a.terminal.WithLock(func(l *terminal.Locked) {
		for _, candidate := range l.Sessions() {
			if candidate != nil && candidate.Serial == serial {
				managed = candidate
				break
			}
		}
		if managed != nil {
			session = l.MarkClosed(managed)
		}
	})
	if managed == nil {
		return false
	}
	if session != nil {
		_ = session.Close()
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

func staleShellCommandFromKind(kind terminal.Kind) string {
	switch kind {
	case terminal.KindOpen:
		return "open"
	case terminal.KindAI:
		return "ai"
	case terminal.KindLocal:
		return "local"
	case terminal.KindCommand:
		return "command"
	case terminal.KindContributeERun, terminal.KindContributeAI, terminal.KindOrchestrator:
		fallthrough
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
