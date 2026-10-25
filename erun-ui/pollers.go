package main

import (
	"time"

	"github.com/sophium/erun/erun-ui/internal/terminal"
)

// startActivityPollers runs the background pollers on the activity service's
// stop channel, so stopping the activity pollers stops them all.
func (a *App) startActivityPollers() {
	stop, started := a.activity.StartPollers()
	if !started {
		return
	}
	// The heartbeat poller catches sessions alive but silent; the stale detector catches dead ones.
	go a.runSessionHeartbeatPoller(stop)
	// Sweeps every configured environment, including ones driven from the CLI or in-pod agents.
	go a.runtime.RunActivityPoller(stop)
	// Usage readings cost a kubectl exec, so they run on a slower cadence.
	go a.runtime.RunUsagePoller(stop)
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
