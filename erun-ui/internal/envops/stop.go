package envops

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Waking has no desktop path: opening an environment runs `erun open`, which wakes it.

// EnvironmentStopResult is what the Runtime tab's Stop control reports back.
type EnvironmentStopResult struct {
	Tenant         string `json:"tenant"`
	Environment    string `json:"environment"`
	Release        string `json:"release"`
	Namespace      string `json:"namespace"`
	AlreadyStopped bool   `json:"alreadyStopped"`
	// RemainingComponents names components the stop deliberately left running.
	RemainingComponents []string `json:"remainingComponents,omitempty"`
}

// RuntimeRunState lets Stop be offered only when the Deployment wants pods.
// On a failed read Present is false and Message is set; that is not "not deployed".
type RuntimeRunState struct {
	Tenant          string `json:"tenant"`
	Environment     string `json:"environment"`
	Present         bool   `json:"present"`
	DesiredReplicas int    `json:"desiredReplicas"`
	ReadyReplicas   int    `json:"readyReplicas"`
	// Stopped is distinct from pods that exist but are not ready.
	Stopped bool `json:"stopped"`
	// RemainingComponents names what a stop would leave running.
	RemainingComponents []string `json:"remainingComponents,omitempty"`
	Message             string   `json:"message,omitempty"`
}

// StopEnvironment records the stop on the env config too, so a later deploy
// keeps it stopped.
func (s *Service) StopEnvironment(selection uimodel.Selection) (EnvironmentStopResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("stop environment", selection.Tenant, selection.Environment); err != nil {
		return EnvironmentStopResult{}, err
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return EnvironmentStopResult{}, err
	}

	// Latch before scaling: dropping the pod wakes every tab's reconnect loop, and
	// `erun open` would undo the stop.
	s.markRuntimeStopped(selection)
	stopped, err := s.d.StopRuntime(core.Context{}, lifecycle.StopEnvironmentParams{
		Result:        result,
		SaveEnvConfig: s.d.Store.SaveEnvConfig,
	})
	if err != nil {
		s.ClearRuntimeStopped(selection)
		return EnvironmentStopResult{}, err
	}

	s.emitEnvStatus(selection, uimodel.EnvStatusRuntimeStopped)
	s.notify("info", stopEnvironmentNotice(stopped))
	return EnvironmentStopResult{
		Tenant:              stopped.Tenant,
		Environment:         stopped.Environment,
		Release:             stopped.Release,
		Namespace:           stopped.Namespace,
		AlreadyStopped:      stopped.AlreadyStopped,
		RemainingComponents: stopped.RemainingComponents,
	}, nil
}

// stopEnvironmentNotice names the recovery action, the sessions ended, and the
// platform components left running.
func stopEnvironmentNotice(result model.StopEnvironmentResult) string {
	kept := stopEnvironmentKeptComponents(result)
	if result.AlreadyStopped {
		return fmt.Sprintf("%s/%s was already stopped.%s Open it to start it again.", result.Tenant, result.Environment, kept)
	}
	sessions := ""
	if len(result.EndedSessions) > 0 {
		sessions = fmt.Sprintf(" %d attached session(s) ended with the pod.", len(result.EndedSessions))
	}
	return fmt.Sprintf("Stopped %s/%s and returned its runtime's capacity to the node.%s%s Open it to start it again.",
		result.Tenant, result.Environment, sessions, kept)
}

func stopEnvironmentKeptComponents(result model.StopEnvironmentResult) string {
	if len(result.RemainingComponents) == 0 {
		return ""
	}
	return fmt.Sprintf(" Its platform component(s) keep running and keep holding capacity: %s.",
		strings.Join(result.RemainingComponents, ", "))
}

// markRuntimeStopped latches a per-env stop the cluster read cannot yet see.
func (s *Service) markRuntimeStopped(selection uimodel.Selection) {
	key := uimodel.SelectionKey(selection)
	s.mu.Lock()
	s.runtimeStops[key] = struct{}{}
	s.mu.Unlock()
}

// ClearRuntimeStopped drops the stop latch; an explicit open is the way back.
func (s *Service) ClearRuntimeStopped(selection uimodel.Selection) {
	key := uimodel.SelectionKey(selection)
	s.mu.Lock()
	delete(s.runtimeStops, key)
	s.mu.Unlock()
}

// isRuntimeStopped does not consume the latch: every tab of the env must see it.
func (s *Service) isRuntimeStopped(selection uimodel.Selection) bool {
	key := uimodel.SelectionKey(selection)
	s.mu.Lock()
	_, ok := s.runtimeStops[key]
	s.mu.Unlock()
	return ok
}

// RuntimeStopped trusts the cluster, not the env config; an unreadable cluster
// reads as not stopped.
func (s *Service) RuntimeStopped(selection uimodel.Selection) bool {
	if s.isRuntimeStopped(selection) {
		return true
	}
	_, state, err := s.readRuntimeRunStateForSelection(selection)
	if err != nil {
		return false
	}
	// Ignore ready count: during termination grace it would disagree with
	// `erun open` and let a respawn undo the stop.
	return state.Stopped()
}

// readRuntimeRunStateForSelection is shared so the gate and the Runtime tab
// resolve the same Deployment.
func (s *Service) readRuntimeRunStateForSelection(selection uimodel.Selection) (config.OpenResult, lifecycle.RuntimeRunState, error) {
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      strings.TrimSpace(selection.Tenant),
		Environment: strings.TrimSpace(selection.Environment),
	})
	if err != nil {
		return config.OpenResult{}, lifecycle.RuntimeRunState{}, err
	}
	state, err := s.d.ReadRuntimeRunState(
		core.Context{},
		lifecycle.RuntimeScaleTargetForResult(result),
	)
	return result, state, err
}

// LoadRuntimeRunState is fail-soft: an unreadable cluster sets Message, which is
// distinct from Present=false.
func (s *Service) LoadRuntimeRunState(selection uimodel.Selection) (RuntimeRunState, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("load runtime run state", selection.Tenant, selection.Environment); err != nil {
		return RuntimeRunState{}, err
	}
	state := RuntimeRunState{Tenant: selection.Tenant, Environment: selection.Environment}
	// Check the latch first, or the panel flashes "running" right after a stop.
	if s.isRuntimeStopped(selection) {
		state.Present = true
		state.Stopped = true
		state.RemainingComponents = s.runtimeStopRemainingComponents(selection)
		return state, nil
	}
	resolved, read, err := s.readRuntimeRunStateForSelection(selection)
	if err != nil {
		state.Message = fmt.Sprintf("Cannot read this environment's runtime run state: %v", err)
		return state, nil
	}
	state.Present = read.Present
	state.DesiredReplicas = read.DesiredReplicas
	state.ReadyReplicas = read.ReadyReplicas
	state.Stopped = read.Stopped()
	state.RemainingComponents = lifecycle.StopRemainingComponents(resolved)
	return state, nil
}

// runtimeStopRemainingComponents returns no names on a failed resolve.
func (s *Service) runtimeStopRemainingComponents(selection uimodel.Selection) []string {
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      strings.TrimSpace(selection.Tenant),
		Environment: strings.TrimSpace(selection.Environment),
	})
	if err != nil {
		return nil
	}
	return lifecycle.StopRemainingComponents(result)
}

// ResolveUpgradePlan builds the read-only "Upgrade all" preview using the shared
// resolver, so it never promises what the run would refuse.
func (s *Service) ResolveUpgradePlan() (lifecycle.UpgradePlan, error) {
	resolver := lifecycle.UpgradeVersionsResolverForStore(s.d.Store, s.d.ResolveImageRegistry)
	return lifecycle.BuildUpgradePlan(s.d.Store, model.UpgradeTarget{}, resolver)
}
