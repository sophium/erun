package main

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

const activityPollerInterval = 5 * time.Second

func (a *App) startActivityPollers() {
	var stop chan struct{}
	a.terminal.WithLock(func(*terminal.Locked) {
		if a.activityPollersStop != nil {
			return
		}
		stop = make(chan struct{})
		a.activityPollersStop = stop
	})
	if stop == nil {
		return
	}

	a.seedConfiguredKubeContexts()

	go a.runHelmReleasePoller(stop)
	go a.runStaleShellDetector(stop)
	// The heartbeat poller catches sessions alive but silent; the stale detector catches dead ones.
	go a.runSessionHeartbeatPoller(stop)
	// Sweeps every configured environment, including ones driven from the CLI or in-pod agents.
	go a.runtime.RunActivityPoller(stop)
	// Usage readings cost a kubectl exec, so they run on a slower cadence.
	go a.runtime.RunUsagePoller(stop)
}

// stopActivityPollers is idempotent.
func (a *App) stopActivityPollers() {
	var stop chan struct{}
	a.terminal.WithLock(func(*terminal.Locked) {
		stop = a.activityPollersStop
		a.activityPollersStop = nil
	})
	if stop != nil {
		close(stop)
	}
}

// seedConfiguredKubeContexts surfaces deploys left pending by a previous run;
// store errors are non-fatal.
func (a *App) seedConfiguredKubeContexts() {
	if a.deps.store == nil {
		return
	}
	result, err := lifecycle.ResolveListResult(a.deps.store, a.deps.findProjectRoot, model.OpenParams{})
	if err != nil {
		return
	}
	a.activityWatchedContextsMu.Lock()
	defer a.activityWatchedContextsMu.Unlock()
	if a.activityWatchedContexts == nil {
		a.activityWatchedContexts = make(map[string]struct{})
	}
	for _, tenant := range result.Tenants {
		for _, env := range tenant.Environments {
			if name := strings.TrimSpace(env.KubernetesContext); name != "" {
				a.activityWatchedContexts[name] = struct{}{}
			}
		}
	}
}

func (a *App) rememberKubeContextForActivity(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	a.activityWatchedContextsMu.Lock()
	defer a.activityWatchedContextsMu.Unlock()
	if a.activityWatchedContexts == nil {
		a.activityWatchedContexts = make(map[string]struct{})
	}
	a.activityWatchedContexts[name] = struct{}{}
}

func (a *App) snapshotConfiguredKubeContexts() []string {
	a.activityWatchedContextsMu.Lock()
	defer a.activityWatchedContextsMu.Unlock()
	out := make([]string, 0, len(a.activityWatchedContexts))
	for name := range a.activityWatchedContexts {
		out = append(out, name)
	}
	return out
}

// watchedKubeContexts limits the helm poller to contexts in use.
func (a *App) watchedKubeContexts() []string {
	seen := make(map[string]struct{})
	var contexts []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		contexts = append(contexts, name)
	}
	for _, name := range a.snapshotConfiguredKubeContexts() {
		add(name)
	}
	if a.activityQueue != nil {
		for _, entry := range a.activityQueue.list() {
			if entry.Status == activityQueueStatusRunning {
				add(entry.KubernetesContext)
			}
		}
	}
	a.terminal.WithLock(func(l *terminal.Locked) {
		for _, managed := range l.Sessions() {
			if managed == nil || managed.Closed {
				continue
			}
			add(managed.Selection.KubernetesContext)
		}
	})
	return contexts
}

// runSessionHeartbeatPoller drives the per-tick reconciles in their order: each
// reads what the one before it just observed.
func (a *App) runSessionHeartbeatPoller(stop <-chan struct{}) {
	ticker := time.NewTicker(terminal.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.terminal.ReconcileSessionHeartbeatsOnce()
			// After the heartbeats, so this tick's pod view is in hand.
			a.terminal.ReconcileAISessionStatusesOnce()
			// Re-enumerated every tick: an env linked while the app runs gets no sync worker
			// otherwise. Idempotent once every linked env is syncing.
			a.envops.ReconcileConfiguredWorkspaceSyncs()
			a.reconcileOrchestratorActivity()
			// After reconcileOrchestratorActivity so shellRunning is this tick's report.
			a.reconcileOrchestratorPacing()
			// Reaps reports for orchestrator ids no longer in a.orchestrators.
			a.reconcileOrphanedOrchestratorShellActivity(time.Now())
		}
	}
}
