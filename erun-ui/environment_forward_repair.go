package main

import (
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/mcp"
)

// Repairs a lost MCP port-forward (dropped or stale) via `erun open --reconnect`,
// which owns the kubectl lifecycle. The repair is bounded, idempotent through
// ensureEnvRuntimeOnce, and never wakes a stopped environment.

// forwardRepairAttempts gives a mid-replacement pod time to return.
const forwardRepairAttempts = 3

// forwardRepairEpisode is keyed by port: a new local port is a new forward.
type forwardRepairEpisode struct {
	port     int
	attempts int
	reported bool
}

// reconcileForwardHealth reports whether the row must render a diagnosed outage.
func (a *App) reconcileForwardHealth(selection uiSelection, port int, portIsBound bool) bool {
	const established = true
	// Only a bound port whose edge answers counts as recovery.
	answers := portIsBound && a.mcpEdgeAnswers(port)
	if answers {
		a.noteOrchestratorEdgeAnswering(selection)
	}
	health := kube.ClassifyPortForward(established, portIsBound, answers)
	if !health.Interrupted() {
		a.forgetForwardRepair(selection)
		return false
	}
	return a.repairInterruptedPortForward(selection, port, health)
}

// mcpEdgeAnswers counts any HTTP reply, 401 included; a missing probe reads as
// answering so an unknown never triggers a restart.
func (a *App) mcpEdgeAnswers(port int) bool {
	return a.deps.canReachMCPEndpoint == nil || a.deps.canReachMCPEndpoint(port)
}

// repairInterruptedPortForward runs only on the single sweep goroutine, so the
// read-decide-record sequence needs no spanning lock.
func (a *App) repairInterruptedPortForward(selection uiSelection, port int, health kube.PortForwardHealth) bool {
	// Ask the cluster only on this path; a stop looks like a dropped forward.
	if a.runtimeStoppedForSelection(selection) {
		a.forgetForwardRepair(selection)
		return false
	}
	episode := a.forwardRepairEpisodeFor(selection, port)
	if episode.attempts < forwardRepairAttempts {
		// Count only a rebind this call started; one in flight is this sweep's attempt.
		if a.ensureEnvRuntime(selection, envEnsureForBrokenForward) {
			a.recordForwardRepairAttempt(selection)
		}
		return false
	}
	if !episode.reported {
		a.recordForwardRepairReported(selection)
		a.emitEnvNotification("warning", selection.Tenant, selection.Environment,
			notificationSourceForwardOutage,
			mcp.DescribeUnrepairedPortForward(selection.Tenant, selection.Environment, port, episode.attempts, health),
			"")
	}
	return true
}

func (a *App) forwardRepairEpisodeFor(selection uiSelection, port int) forwardRepairEpisode {
	key := selectionKey(normalizeSelection(selection))
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.forwardRepairs == nil {
		a.forwardRepairs = make(map[string]forwardRepairEpisode)
	}
	episode, ok := a.forwardRepairs[key]
	if !ok || episode.port != port {
		episode = forwardRepairEpisode{port: port}
		a.forwardRepairs[key] = episode
	}
	return episode
}

func (a *App) recordForwardRepairAttempt(selection uiSelection) {
	a.updateForwardRepairEpisode(selection, func(episode *forwardRepairEpisode) {
		episode.attempts++
	})
}

// recordForwardRepairReported latches the episode's single report.
func (a *App) recordForwardRepairReported(selection uiSelection) {
	a.updateForwardRepairEpisode(selection, func(episode *forwardRepairEpisode) {
		episode.reported = true
	})
}

func (a *App) updateForwardRepairEpisode(selection uiSelection, mutate func(*forwardRepairEpisode)) {
	key := selectionKey(normalizeSelection(selection))
	a.mu.Lock()
	defer a.mu.Unlock()
	episode, ok := a.forwardRepairs[key]
	if !ok {
		return
	}
	mutate(&episode)
	a.forwardRepairs[key] = episode
}

// forgetForwardRepair ends the episode and retires its notification.
func (a *App) forgetForwardRepair(selection uiSelection) {
	selection = normalizeSelection(selection)
	key := selectionKey(selection)
	a.mu.Lock()
	episode, ok := a.forwardRepairs[key]
	delete(a.forwardRepairs, key)
	a.mu.Unlock()
	if ok && episode.reported {
		a.emitClearEnvNotification(selection.Tenant, selection.Environment, notificationSourceForwardOutage)
	}
}
