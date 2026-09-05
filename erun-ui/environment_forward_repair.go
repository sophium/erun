package main

import (
	eruncommon "github.com/sophium/erun/erun-common"
)

// environment_forward_repair.go repairs the failure the activity sweep is the
// first to see and the last thing that ever noticed: an environment that was
// open loses the port-forward carrying its MCP edge, and nothing starts a
// replacement. The environment is then unreachable to every client for as long
// as the operator does not notice — five hours, in the case this was written
// for — because nothing that decides reachability from a held port has any
// reason to act.
//
// Both shapes of that loss are repaired here, and the pairing is the point. A
// forward whose pod was replaced usually makes kubectl exit outright, leaving
// the port free (dropped); occasionally the listener survives its far end and
// answers nothing through it (stale). The second is the weirder one and the one
// that took hours to diagnose, but the first is what every ordinary rollout,
// scale or eviction produces, so a repair that acts only on the rare shape
// leaves the common one silent.
//
// The repair is `erun open --reconnect`, driven through the same ensure-runtime
// path a tab spawn uses. That command already owns the kubectl lifecycle: it
// recognises its own forward, stops a stale one, and starts a replacement.
// Starting a forward from here instead would put two owners on one port.
//
// Three properties make the repair safe to run unattended, and each is load
// bearing:
//   - Bounded. An episode gets a fixed number of attempts and then reports.
//     A forward that a fresh open cannot fix is broken somewhere this cannot
//     reach, and retrying it forever would be an invisible loop against a
//     cluster.
//   - Idempotent. ensureEnvRuntimeOnce holds the in-flight latch and the
//     success window, so overlapping sweeps produce one reconnect, never a
//     second forward racing the first for the port.
//   - Never a resurrection. A stopped environment is not a broken one; its
//     forward is *supposed* to be dead. It ends the episode instead of starting
//     one, and the reconnect itself refuses to wake a stopped runtime. The
//     environment nobody ever opened is out of reach by construction: it has no
//     recorded forward, so the sweep never arrives here for it at all.

// forwardRepairAttempts bounds one episode. Three sweeps is long enough for a
// pod that is merely mid-replacement to come back on its own — the forward
// re-establishes as soon as the new pod is ready — and short enough that a
// genuinely dead one is named within a couple of minutes rather than an
// afternoon.
const forwardRepairAttempts = 3

// forwardRepairEpisode is what one environment's broken forward has cost so
// far. The port is part of it because a forward on a different local port is a
// different forward: re-opening an environment that picked a new local port
// starts a fresh episode rather than inheriting the old one's exhausted
// attempts.
type forwardRepairEpisode struct {
	port     int
	attempts int
	reported bool
}

// reconcileForwardHealth turns one sweep's observations of an environment's MCP
// forward into the response they call for, and reports whether the row must
// render a diagnosed outage rather than a quiet environment.
//
// The caller arrives here having already established that a forward is recorded
// for this environment — the record is what separates "was open" from "nobody
// opened it" — and whether anything still holds its local port. Only the edge
// question is left, and only when there is a listener to ask.
func (a *App) reconcileForwardHealth(selection uiSelection, port int, portIsBound bool) bool {
	const established = true
	health := eruncommon.ClassifyPortForward(established, portIsBound, portIsBound && a.mcpEdgeAnswers(port))
	if !health.Interrupted() {
		a.forgetForwardRepair(selection)
		return false
	}
	return a.repairInterruptedPortForward(selection, port, health)
}

// mcpEdgeAnswers reports whether anything at all replies through the forward.
// Any HTTP answer counts, a 401 included: the question is whether the tunnel
// carries traffic, not whether this caller is allowed through it. A missing
// probe cannot tell, and "cannot tell" must not read as broken — that would
// turn every edge that merely refused an idle query into a restart.
func (a *App) mcpEdgeAnswers(port int) bool {
	return a.deps.canReachMCPEndpoint == nil || a.deps.canReachMCPEndpoint(port)
}

// repairInterruptedPortForward handles one sweep's observation that the
// environment has lost its forward, and reports whether the row must now render
// that as a diagnosed outage.
//
// It is called only from the activity sweep, which is a single goroutine, so
// the read-decide-record sequence below needs no lock spanning the three; each
// step takes the lock for its own map access.
func (a *App) repairInterruptedPortForward(selection uiSelection, port int, health eruncommon.PortForwardHealth) bool {
	// Ask the cluster only on this path. A stopped environment reaching here is
	// the expected shape of a stop, not a fault, so the read is worth its cost
	// exactly when something looks wrong — never on every sweep of every
	// environment.
	if a.runtimeStoppedForSelection(selection) {
		a.forgetForwardRepair(selection)
		return false
	}
	episode := a.forwardRepairEpisodeFor(selection, port)
	if episode.attempts < forwardRepairAttempts {
		// Count only a rebind this call actually started. One already in flight
		// *is* this sweep's attempt, and counting it twice would exhaust the
		// episode against a repair that was never given time to finish.
		if a.ensureEnvRuntime(selection, envEnsureForBrokenForward) {
			a.recordForwardRepairAttempt(selection)
		}
		return false
	}
	if !episode.reported {
		a.recordForwardRepairReported(selection)
		a.emitEnvNotification("warning", selection.Tenant, selection.Environment,
			notificationSourceForwardOutage,
			eruncommon.DescribeUnrepairedPortForward(selection.Tenant, selection.Environment, port, episode.attempts, health),
			"")
	}
	return true
}

// forwardRepairEpisodeFor reads the environment's episode, starting a fresh one
// when the forward is on a different local port than the episode was opened
// against — that is a different forward, not the same one still failing.
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

// recordForwardRepairReported latches the one report an episode gets, so a
// forward that stays broken does not re-post its notification every sweep.
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

// forgetForwardRepair ends the episode, and retires its notification when there
// was one to retire. Called for every observation that is not a broken forward —
// a serving edge, a forward that was never established, a stopped environment —
// so the next failure is counted and reported from zero.
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
