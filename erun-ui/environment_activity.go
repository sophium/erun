package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// environment_activity.go answers the question the sidebar could not answer: is
// this environment being used, and by what. Two gaps produced the same blank
// row. An environment driven entirely from the CLI or an agent had no desktop
// session, so nothing rendered even though its forwards were up and its edge was
// answering — "open" meant "the desktop started it", not "in use". And work
// inside the pod was invisible regardless, because the only signals the desktop
// watched were its own deploys and its own terminals.
//
// So the observation is taken from the environment itself: the edge answers or
// it does not (reachable), and the environment's own idle markers — including
// any held work lease — say whether something is running (busy) and what.
//
// It is deliberately separate from the env-status condition. A condition is
// sticky and set by a lifecycle event the desktop drove (a stop, a failed
// deploy); this is a fresh observation replaced on every tick. The sidebar
// derives one state from both, so there is still a single state model.

// environmentActivityInterval paces the sweep. Each environment costs one
// loopback dial, and an HTTP call only for the ones that answer, so this stays
// cheap for an operator with a handful of environments while surfacing a
// started build within one tick.
const environmentActivityInterval = 20 * time.Second

// environmentActivityTimeout keeps a wedged edge from stalling the sweep.
const environmentActivityTimeout = 5 * time.Second

// environmentActivity is one environment's observed condition.
type environmentActivity struct {
	selection uiSelection
	// state is the observation itself, split out as a comparable value so the
	// sweep can tell a transition from a restatement.
	state environmentActivityState
}

type environmentActivityState struct {
	reachable bool
	// observed records that the environment answered the idle question, as
	// opposed to busy's false meaning nobody got an answer. The two are not the
	// same claim, and the sidebar clears a stale desktop latch on the strength
	// of this one — a wedged edge whose port still answers must not be able to
	// say "idle" on the environment's behalf.
	observed bool
	busy     bool
	// outage is the environment having lost the forward it had, past the point
	// a bounded repair could bring it back — the local port free, or held by
	// something that answers nothing. The row has to name it, because every
	// other field here renders such an environment exactly like an idle one, or
	// like one nobody ever opened.
	outage bool
	// detail names what is keeping the environment busy, in the operator's
	// language, so the row can say "held by gradle-build" rather than "busy".
	detail string
}

func (a *App) runEnvironmentActivityPoller(stop <-chan struct{}) {
	// No eager first sweep: boot is the one moment the desktop is busiest, and a
	// status that arrives one tick late costs nothing.
	ticker := time.NewTicker(environmentActivityInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.reconcileEnvironmentActivityOnce()
		}
	}
}

func (a *App) reconcileEnvironmentActivityOnce() {
	for _, selection := range a.configuredSelections() {
		a.emitEnvActivityIfChanged(a.observeEnvironmentActivity(selection))
	}
}

// configuredSelections lists every environment in the config store, not only the
// ones with desktop tabs — an environment nobody opened here is exactly the case
// this poller exists to make visible.
func (a *App) configuredSelections() []uiSelection {
	if a.deps.store == nil {
		return nil
	}
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return nil
	}
	var out []uiSelection
	for _, tenant := range tenants {
		envs, err := a.deps.store.ListEnvConfigs(tenant.Name)
		if err != nil {
			continue
		}
		for _, env := range envs {
			out = append(out, uiSelection{Tenant: tenant.Name, Environment: env.Name})
		}
	}
	return out
}

// observeEnvironmentActivity is deliberately cheap for the common case. An
// environment nobody opened costs one small file read and nothing else — no
// config resolution, no dial, no HTTP — because most environments in a
// configured store are not running at any given moment, and this sweep runs
// forever beside the desktop's own work.
func (a *App) observeEnvironmentActivity(selection uiSelection) environmentActivity {
	observation := environmentActivity{selection: selection}
	// Reachability is "a forward was established for this environment and its
	// edge answers" — not "some process holds that port". The state file is what
	// distinguishes the two, and `erun open` writes it whoever ran it, which is
	// what makes a CLI-opened environment visible here at all. It is also the
	// only record that this environment was ever open, so its absence is the
	// line the sweep does not cross: an environment nobody opened is left alone.
	forward, established, err := eruncommon.LoadPortForwardState("mcp", selection.Tenant, selection.Environment)
	if err != nil || !established {
		a.forgetForwardRepair(selection)
		return observation
	}
	if a.deps.canConnectLocalPort == nil {
		// Nothing was observed, so there is nothing to diagnose. An unanswerable
		// question must not be recorded as a bad answer.
		a.forgetForwardRepair(selection)
		return observation
	}
	if !a.deps.canConnectLocalPort(forward.LocalPort) {
		// The dropped forward — and the reason this sweep used to stop here.
		// Every ordinary pod replacement makes kubectl exit outright, so the
		// port is simply free afterwards and the environment is unreachable to
		// every client of it. Returning "not reachable" and nothing else was
		// true and useless: it renders as an environment nobody opened, which
		// is the one thing this environment is not.
		observation.state.outage = a.reconcileForwardHealth(selection, forward.LocalPort, false)
		return observation
	}
	observation.state.reachable = true

	if a.deps.loadIdleStatus == nil {
		return observation
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, environmentActivityTimeout)
	defer cancel()
	// Address the port the forward actually bound, not the one the config would
	// resolve today: a config edit since the forward was established would
	// otherwise send the probe somewhere else.
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp", forward.LocalPort)
	status, err := a.deps.loadIdleStatus(ctx, endpoint, a.mcpBearer(selection.Tenant, selection.Environment))
	if err != nil {
		// The port answered but the edge did not. That is not evidence of idle,
		// so report reachable-without-a-verdict rather than inventing one — and
		// then find out which of the two failures it was, because they need
		// opposite responses. An edge that replies at all (a 401 counts) is a
		// live tunnel whose idle question failed and must be left alone; an edge
		// that replies to nothing is a forward pointed at a pod that is gone.
		observation.state.outage = a.reconcileForwardHealth(selection, forward.LocalPort, true)
		return observation
	}
	a.forgetForwardRepair(selection)
	observation.state.observed = true
	observation.state.busy, observation.state.detail = environmentBusyFromIdleStatus(status)
	return observation
}

// environmentBusyFromIdleStatus reduces the environment's own markers to the one
// line the sidebar shows. A held lease wins because it is the only signal that
// names the work; everything else is described by what the marker means rather
// than by its wire name.
func environmentBusyFromIdleStatus(status eruncommon.EnvironmentIdleStatus) (bool, string) {
	if len(status.Leases) > 0 {
		names := make([]string, 0, len(status.Leases))
		for _, lease := range status.Leases {
			if name := strings.TrimSpace(lease.Name); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			return true, "holding: " + strings.Join(names, ", ")
		}
	}
	for _, marker := range status.Markers {
		if marker.Name == "working-hours" || marker.Idle {
			continue
		}
		if detail := eruncommon.EnvironmentActivityMarkerDetail(marker.Name); detail != "" {
			return true, detail
		}
	}
	return false, ""
}

// emitEnvActivityIfChanged publishes only transitions. Most environments in a
// configured store are quiet, and re-announcing "still quiet" for each of them
// on every tick would put a steady stream of no-op events through the event
// bridge and the frontend store for no rendered difference.
func (a *App) emitEnvActivityIfChanged(observation environmentActivity) {
	key := selectionKey(observation.selection)
	a.mu.Lock()
	previous, seen := a.envActivity[key]
	unchanged := seen && previous == observation.state
	if !unchanged {
		if a.envActivity == nil {
			a.envActivity = make(map[string]environmentActivityState)
		}
		a.envActivity[key] = observation.state
	}
	a.mu.Unlock()
	if unchanged {
		return
	}
	a.emitEnvActivity(observation)
}

func (a *App) emitEnvActivity(observation environmentActivity) {
	a.emitEvent(envActivityEvent, envActivityPayload{
		Tenant:      observation.selection.Tenant,
		Environment: observation.selection.Environment,
		Reachable:   observation.state.reachable,
		Observed:    observation.state.observed,
		Outage:      observation.state.outage,
		Busy:        observation.state.busy,
		Detail:      observation.state.detail,
	})
}
