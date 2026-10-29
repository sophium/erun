package runtimeview

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/idlestatus"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Activity is observed from the environment itself (edge reachability, idle
// markers, leases), separately from the sticky env-status condition.

const environmentActivityInterval = 20 * time.Second

const environmentActivityTimeout = 5 * time.Second

type envObservation struct {
	selection uimodel.Selection
	// state is comparable so the sweep can tell a transition from a restatement.
	state ActivityState
}

type ActivityState struct {
	Reachable bool
	// Observed means the environment answered the idle question; only this may
	// clear a stale desktop latch.
	Observed bool
	Busy     bool
	// Outage: the forward was lost beyond what a bounded repair could restore.
	Outage bool
	// CheckFailed: the pod fallback check was attempted and did not answer, which
	// differs from never checking.
	CheckFailed bool
	Detail      string
	// BusyHolderOrchestrators is a sorted, comma-joined string so the state stays
	// comparable with ==.
	BusyHolderOrchestrators string
}

// RunActivityPoller sweeps until stop closes.
func (s *Service) RunActivityPoller(stop <-chan struct{}) {
	// No eager first sweep: boot is the desktop's busiest moment.
	ticker := time.NewTicker(environmentActivityInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.reconcileEnvironmentActivityOnce()
		}
	}
}

func (s *Service) reconcileEnvironmentActivityOnce() {
	for _, selection := range s.configuredSelections() {
		observation := s.observeEnvironmentActivity(selection)
		s.PublishActivity(observation.selection, observation.state)
	}
}

// TriggerEnvironmentActivitySweep runs one sweep synchronously so tests can
// consume the first-observation emit before staging state.
func (s *Service) TriggerEnvironmentActivitySweep() {
	s.reconcileEnvironmentActivityOnce()
}

// ResetEnvironmentActivityObservations discards cached observations; used by the
// Playwright harness, whose seeded rows outlive a single spec.
func (s *Service) ResetEnvironmentActivityObservations() {
	s.mu.Lock()
	s.activity = nil
	s.mu.Unlock()
}

// configuredSelections lists every configured environment, not only open tabs.
func (s *Service) configuredSelections() []uimodel.Selection {
	tenants, err := s.d.Store.ListTenantConfigs()
	if err != nil {
		return nil
	}
	var out []uimodel.Selection
	for _, tenant := range tenants {
		envs, err := s.d.Store.ListEnvConfigs(tenant.Name)
		if err != nil {
			continue
		}
		for _, env := range envs {
			out = append(out, uimodel.Selection{Tenant: tenant.Name, Environment: env.Name})
		}
	}
	return out
}

// ObserveActivity takes one reading without recording or publishing it.
func (s *Service) ObserveActivity(selection uimodel.Selection) ActivityState {
	return s.observeEnvironmentActivity(selection).state
}

func (s *Service) observeEnvironmentActivity(selection uimodel.Selection) envObservation {
	observation := envObservation{selection: selection}
	// The state file, written by `erun open` whoever ran it, distinguishes an
	// established forward from any process holding the port.
	forward, established, err := kube.LoadPortForwardState("mcp", selection.Tenant, selection.Environment)
	if err != nil || !established {
		s.d.ForgetForwardRepair(selection)
		return s.observeEnvironmentActivityViaPod(selection, observation)
	}
	if !s.d.CanConnectLocalPort(forward.LocalPort) {
		// A dropped forward must render as an outage, not as never opened.
		observation.state.Outage = s.d.ReconcileForwardHealth(selection, forward.LocalPort, false)
		return observation
	}
	observation.state.Reachable = true

	parent := s.d.Context()
	ctx, cancel := context.WithTimeout(parent, environmentActivityTimeout)
	defer cancel()
	// Use the bound port, not today's config, which may have changed since.
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/mcp", forward.LocalPort)
	status, err := s.d.LoadIdleStatus(ctx, endpoint, s.d.MCPBearer(selection.Tenant, selection.Environment))
	if err != nil {
		// The port answered but the edge did not: an edge that replies at all is a
		// live tunnel; one that replies to nothing points at a gone pod.
		observation.state.Outage = s.d.ReconcileForwardHealth(selection, forward.LocalPort, true)
		return observation
	}
	s.d.ForgetForwardRepair(selection)
	// A real answer through the forward ends any recorded outage.
	s.d.NoteEdgeAnswering(selection)
	observation.state.Observed = true
	observation.state.Busy, observation.state.Detail = environmentBusyFromIdleStatus(status)
	observation.state.BusyHolderOrchestrators = environmentLeaseHolderOrchestrators(status.Leases)
	return observation
}

const environmentActivityPodProbeTimeout = 8 * time.Second

// observeEnvironmentActivityViaPod reads the activity lease over kubectl exec
// when this desktop has no local forward; no forward does not mean unused.
func (s *Service) observeEnvironmentActivityViaPod(selection uimodel.Selection, observation envObservation) envObservation {
	envConfig, _, err := s.d.Store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil || strings.TrimSpace(envConfig.KubernetesContext) == "" {
		return observation
	}
	parent := s.d.Context()
	ctx, cancel := context.WithTimeout(parent, environmentActivityPodProbeTimeout)
	defer cancel()
	script := fmt.Sprintf("erun idle %s %s --json", shellQuote(selection.Tenant), shellQuote(selection.Environment))
	output, err := s.d.ExecRuntimePod(ctx, selection, script)
	if err != nil {
		observation.state.CheckFailed = true
		return observation
	}
	var status model.EnvironmentIdleStatus
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		observation.state.CheckFailed = true
		return observation
	}
	observation.state.Reachable = true
	observation.state.Observed = true
	observation.state.Busy, observation.state.Detail = environmentBusyFromIdleStatus(status)
	observation.state.BusyHolderOrchestrators = environmentLeaseHolderOrchestrators(status.Leases)
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
		if detail := idlestatus.EnvironmentActivityMarkerDetail(marker.Name); detail != "" {
			return true, detail
		}
	}
	return false, ""
}

// PublishActivity records an observation and emits it only on a transition.
func (s *Service) PublishActivity(selection uimodel.Selection, state ActivityState) {
	key := uimodel.SelectionKey(selection)
	s.mu.Lock()
	previous, seen := s.activity[key]
	unchanged := seen && previous == state
	if !unchanged {
		if s.activity == nil {
			s.activity = make(map[string]ActivityState)
		}
		s.activity[key] = state
	}
	s.mu.Unlock()
	if unchanged {
		return
	}
	s.emitEnvActivity(selection, state)
}

// ActivitySnapshot copies the last observation per selection key, nil when none.
func (s *Service) ActivitySnapshot() map[string]ActivityState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.activity) == 0 {
		return nil
	}
	out := make(map[string]ActivityState, len(s.activity))
	for k, v := range s.activity {
		out[k] = v
	}
	return out
}

// ActivitySnapshotFrom is the read-model form of an observation.
func ActivitySnapshotFrom(state ActivityState) *ActivitySnapshot {
	return &ActivitySnapshot{
		Reachable:   state.Reachable,
		Observed:    state.Observed,
		Outage:      state.Outage,
		CheckFailed: state.CheckFailed,
		Busy:        state.Busy,
		Detail:      state.Detail,
	}
}

func (s *Service) emitEnvActivity(selection uimodel.Selection, state ActivityState) {
	s.d.Emit(uimodel.EnvActivityEvent, ActivityPayload{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Reachable:   state.Reachable,
		Observed:    state.Observed,
		Outage:      state.Outage,
		CheckFailed: state.CheckFailed,
		Busy:        state.Busy,
		Detail:      state.Detail,
	})
}
