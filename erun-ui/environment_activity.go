package main

import (
	"context"
	"encoding/json"
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
	// checkFailed is outage's counterpart for the other channel this poller
	// reads: an environment with no local forward is not left unasked just
	// because this desktop never opened it, but the fallback check (over
	// kubectl exec, see observeEnvironmentActivityViaPod) can itself fail to
	// answer. That must not collapse into the same "nobody opened it" reading
	// a never-checked environment gets — a real attempt that came back empty is
	// its own condition, not silence.
	checkFailed bool
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

// TriggerEnvironmentActivitySweep runs one sweep pass synchronously, the same
// pass runEnvironmentActivityPoller's ticker runs on its own schedule. It
// exists so a test can drive the sweep deterministically instead of waiting
// on a tick it cannot see: emitEnvActivityIfChanged only emits on a
// transition, so the very first observation of an environment this App has
// never observed before always emits once regardless of what it finds, and a
// test that later stages its own activity state for that environment must not
// race that one-time emission. Calling this before staging any state consumes
// it up front, so every later tick of the automatic ticker observes the same
// unchanged environment and stays quiet for the rest of the test.
func (a *App) TriggerEnvironmentActivitySweep() {
	a.reconcileEnvironmentActivityOnce()
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

// observeEnvironmentActivity is deliberately cheap for the common case of an
// environment this desktop has open: one loopback dial, and an HTTP call only
// for the ones that answer. An environment with no local forward is not the
// common case's "leave it alone" anymore (see observeEnvironmentActivityViaPod)
// — the activity lease this poller looks for is environment-side state, not
// desktop-side, so a CLI- or agent-driven environment must be askable too.
func (a *App) observeEnvironmentActivity(selection uiSelection) environmentActivity {
	observation := environmentActivity{selection: selection}
	// Reachability is "a forward was established for this environment and its
	// edge answers" — not "some process holds that port". The state file is what
	// distinguishes the two, and `erun open` writes it whoever ran it, which is
	// what makes a CLI-opened environment visible here at all. Its absence only
	// means this desktop has no local forward; it does not mean nobody is using
	// the environment, so the fallback below still asks.
	forward, established, err := eruncommon.LoadPortForwardState("mcp", selection.Tenant, selection.Environment)
	if err != nil || !established {
		a.forgetForwardRepair(selection)
		return a.observeEnvironmentActivityViaPod(selection, observation)
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

// environmentActivityPodProbeTimeout bounds the kubectl-exec fallback below.
// It is looser than environmentActivityTimeout's loopback-dial budget because
// a pod exec is a real round trip to the cluster's API server, not a dial to
// this machine, but it still has to stay short enough that one wedged cluster
// cannot stall the rest of the sweep for long.
const environmentActivityPodProbeTimeout = 8 * time.Second

// observeEnvironmentActivityViaPod is the fallback for an environment this
// desktop has no local MCP forward for. An operator driving the environment
// from a CLI, or an agent holding it from another machine entirely, is the
// ordinary case erun is built for, not an error — so "no local forward" must
// not read as "nobody is using this". The environment's own activity lease is
// readable straight from its runtime pod, the same way runtime_activity.go
// already reads pod state the MCP edge cannot: over kubectl exec, without
// ever establishing a forward of its own. `erun idle --json` run inside the
// pod answers from the pod's own local store (see erun-cli's
// environmentTargetsItself), so this is one exec, not a repeat of the network
// hop the desktop cannot make.
func (a *App) observeEnvironmentActivityViaPod(selection uiSelection, observation environmentActivity) environmentActivity {
	if a.deps.store == nil {
		return observation
	}
	envConfig, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil || strings.TrimSpace(envConfig.KubernetesContext) == "" {
		// This environment has never named a cluster it could be running in, so
		// there is nothing to ask — "not open here" already says everything
		// there is to say about it.
		return observation
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, environmentActivityPodProbeTimeout)
	defer cancel()
	script := fmt.Sprintf("erun idle %s %s --json", shellQuote(selection.Tenant), shellQuote(selection.Environment))
	output, err := a.execInRuntimePod(ctx, selection, script)
	if err != nil {
		// A real attempt that did not come back is not the same silence as
		// never asking — see checkFailed's own comment.
		observation.state.checkFailed = true
		return observation
	}
	var status eruncommon.EnvironmentIdleStatus
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		observation.state.checkFailed = true
		return observation
	}
	observation.state.reachable = true
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

// seedEnvironmentActivitySnapshots attaches each environment's last poller
// observation, if any, to the initial-state read model. See uiEnvironment's
// Activity field for why this exists: emitEnvActivityIfChanged only fires on
// a transition, so a boot that starts from a clean store (a page reload, not
// a process restart) has no other way to learn an env is already busy.
func (a *App) seedEnvironmentActivitySnapshots(state *uiState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for ti := range state.Tenants {
		tenant := &state.Tenants[ti]
		for ei := range tenant.Environments {
			env := &tenant.Environments[ei]
			key := selectionKey(uiSelection{Tenant: tenant.Name, Environment: env.Name})
			observed, ok := a.envActivity[key]
			if !ok {
				continue
			}
			env.Activity = &uiEnvironmentActivitySnapshot{
				Reachable:   observed.reachable,
				Observed:    observed.observed,
				Outage:      observed.outage,
				CheckFailed: observed.checkFailed,
				Busy:        observed.busy,
				Detail:      observed.detail,
			}
		}
	}
}

// envActivitySnapshot copies the poller's per-environment observations under
// lock, for callers assembling a read model outside any a.mu section of their
// own (a.mu is not reentrant, so a caller already holding it must read
// a.envActivity directly instead of calling this).
func (a *App) envActivitySnapshot() map[string]environmentActivityState {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.envActivity) == 0 {
		return nil
	}
	out := make(map[string]environmentActivityState, len(a.envActivity))
	for k, v := range a.envActivity {
		out[k] = v
	}
	return out
}

func (a *App) emitEnvActivity(observation environmentActivity) {
	a.emitEvent(envActivityEvent, envActivityPayload{
		Tenant:      observation.selection.Tenant,
		Environment: observation.selection.Environment,
		Reachable:   observation.state.reachable,
		Observed:    observation.state.observed,
		Outage:      observation.state.outage,
		CheckFailed: observation.state.checkFailed,
		Busy:        observation.state.busy,
		Detail:      observation.state.detail,
	})
}
