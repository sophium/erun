package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Activity is observed from the environment itself (edge reachability, idle
// markers, leases), separately from the sticky env-status condition.

const environmentActivityInterval = 20 * time.Second

const environmentActivityTimeout = 5 * time.Second

type environmentActivity struct {
	selection uimodel.Selection
	// state is comparable so the sweep can tell a transition from a restatement.
	state environmentActivityState
}

type environmentActivityState struct {
	reachable bool
	// observed means the environment answered the idle question; only this may
	// clear a stale desktop latch.
	observed bool
	busy     bool
	// outage: the forward was lost beyond what a bounded repair could restore.
	outage bool
	// checkFailed: the pod fallback check was attempted and did not answer, which
	// differs from never checking.
	checkFailed bool
	detail      string
	// busyHolderOrchestrators is a sorted, comma-joined string so the state stays
	// comparable with ==.
	busyHolderOrchestrators string
}

func (a *App) runEnvironmentActivityPoller(stop <-chan struct{}) {
	// No eager first sweep: boot is the desktop's busiest moment.
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

// TriggerEnvironmentActivitySweep runs one sweep synchronously so tests can
// consume the first-observation emit before staging state.
func (a *App) TriggerEnvironmentActivitySweep() {
	a.reconcileEnvironmentActivityOnce()
}

// ResetEnvironmentActivityObservations discards cached observations; used by the
// Playwright harness, whose seeded rows outlive a single spec.
func (a *App) ResetEnvironmentActivityObservations() {
	a.mu.Lock()
	a.envActivity = nil
	a.mu.Unlock()
}

// configuredSelections lists every configured environment, not only open tabs.
func (a *App) configuredSelections() []uimodel.Selection {
	if a.deps.store == nil {
		return nil
	}
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return nil
	}
	var out []uimodel.Selection
	for _, tenant := range tenants {
		envs, err := a.deps.store.ListEnvConfigs(tenant.Name)
		if err != nil {
			continue
		}
		for _, env := range envs {
			out = append(out, uimodel.Selection{Tenant: tenant.Name, Environment: env.Name})
		}
	}
	return out
}

func (a *App) observeEnvironmentActivity(selection uimodel.Selection) environmentActivity {
	observation := environmentActivity{selection: selection}
	// The state file, written by `erun open` whoever ran it, distinguishes an
	// established forward from any process holding the port.
	forward, established, err := kube.LoadPortForwardState("mcp", selection.Tenant, selection.Environment)
	if err != nil || !established {
		a.forgetForwardRepair(selection)
		return a.observeEnvironmentActivityViaPod(selection, observation)
	}
	if a.deps.canConnectLocalPort == nil {
		// Nothing was observed, so there is nothing to diagnose.
		a.forgetForwardRepair(selection)
		return observation
	}
	if !a.deps.canConnectLocalPort(forward.LocalPort) {
		// A dropped forward must render as an outage, not as never opened.
		observation.state.outage = a.reconcileForwardHealth(selection, forward.LocalPort, false)
		return observation
	}
	observation.state.reachable = true

	if a.deps.loadIdleStatus == nil {
		return observation
	}
	parent := a.context()
	ctx, cancel := context.WithTimeout(parent, environmentActivityTimeout)
	defer cancel()
	// Use the bound port, not today's config, which may have changed since.
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp", forward.LocalPort)
	status, err := a.deps.loadIdleStatus(ctx, endpoint, a.mcpBearer(selection.Tenant, selection.Environment))
	if err != nil {
		// The port answered but the edge did not: an edge that replies at all is a
		// live tunnel; one that replies to nothing points at a gone pod.
		observation.state.outage = a.reconcileForwardHealth(selection, forward.LocalPort, true)
		return observation
	}
	a.forgetForwardRepair(selection)
	// A real answer through the forward ends any recorded outage.
	a.noteOrchestratorEdgeAnswering(selection)
	observation.state.observed = true
	observation.state.busy, observation.state.detail = environmentBusyFromIdleStatus(status)
	observation.state.busyHolderOrchestrators = environmentLeaseHolderOrchestrators(status.Leases)
	return observation
}

const environmentActivityPodProbeTimeout = 8 * time.Second

// observeEnvironmentActivityViaPod reads the activity lease over kubectl exec
// when this desktop has no local forward; no forward does not mean unused.
func (a *App) observeEnvironmentActivityViaPod(selection uimodel.Selection, observation environmentActivity) environmentActivity {
	if a.deps.store == nil {
		return observation
	}
	envConfig, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil || strings.TrimSpace(envConfig.KubernetesContext) == "" {
		return observation
	}
	parent := a.context()
	ctx, cancel := context.WithTimeout(parent, environmentActivityPodProbeTimeout)
	defer cancel()
	script := fmt.Sprintf("erun idle %s %s --json", shellQuote(selection.Tenant), shellQuote(selection.Environment))
	output, err := a.execInRuntimePod(ctx, selection, script)
	if err != nil {
		observation.state.checkFailed = true
		return observation
	}
	var status model.EnvironmentIdleStatus
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		observation.state.checkFailed = true
		return observation
	}
	observation.state.reachable = true
	observation.state.observed = true
	observation.state.busy, observation.state.detail = environmentBusyFromIdleStatus(status)
	observation.state.busyHolderOrchestrators = environmentLeaseHolderOrchestrators(status.Leases)
	return observation
}

// Leases without a named orchestrator holder contribute nothing, so they never
// suppress an orchestrator's pacing nudge.
func environmentLeaseHolderOrchestrators(leases []model.EnvironmentActivityLease) string {
	seen := make(map[string]struct{})
	for _, lease := range leases {
		if id := strings.TrimSpace(lease.Holder.Orchestrator); id != "" {
			seen[id] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return ""
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

// A held lease wins because it is the only signal that names the work.
func environmentBusyFromIdleStatus(status model.EnvironmentIdleStatus) (bool, string) {
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
		if detail := activity.EnvironmentActivityMarkerDetail(marker.Name); detail != "" {
			return true, detail
		}
	}
	return false, ""
}

// emitEnvActivityIfChanged publishes only transitions.
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

// seedEnvironmentActivitySnapshots lets a reload learn activity that will not
// re-emit until the next transition.
func (a *App) seedEnvironmentActivitySnapshots(state *uiState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for ti := range state.Tenants {
		tenant := &state.Tenants[ti]
		for ei := range tenant.Environments {
			env := &tenant.Environments[ei]
			key := selectionKey(uimodel.Selection{Tenant: tenant.Name, Environment: env.Name})
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

// envActivitySnapshot locks a.mu (not reentrant); callers holding it read a.envActivity directly.
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
	a.emitEvent(uimodel.EnvActivityEvent, envActivityPayload{
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
