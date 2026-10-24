package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/primitive/open"
)

// Waking has no desktop path: opening an environment runs `erun open`, which wakes it.

// StopEnvironment records the stop on the env config too, so a later deploy
// keeps it stopped.
func (a *App) StopEnvironment(selection uiSelection) (uiEnvironmentStopResult, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("stop environment", selection.Tenant, selection.Environment); err != nil {
		return uiEnvironmentStopResult{}, err
	}
	result, err := open.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uiEnvironmentStopResult{}, err
	}

	// Latch before scaling: dropping the pod wakes every tab's reconnect loop, and
	// `erun open` would undo the stop.
	a.markRuntimeStopped(selection)
	stopped, err := a.deps.stopEnvironmentRuntime(core.Context{}, lifecycle.StopEnvironmentParams{
		Result:        result,
		SaveEnvConfig: a.deps.store.SaveEnvConfig,
	})
	if err != nil {
		a.clearRuntimeStopped(selection)
		return uiEnvironmentStopResult{}, err
	}

	a.emitEnvStatus(selection, envStatusRuntimeStopped)
	a.emitAppNotification("info", stopEnvironmentNotice(stopped))
	return uiEnvironmentStopResult{
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
func (a *App) markRuntimeStopped(selection uiSelection) {
	key := selectionKey(selection)
	a.mu.Lock()
	a.runtimeStops[key] = struct{}{}
	a.mu.Unlock()
}

func (a *App) clearRuntimeStopped(selection uiSelection) {
	key := selectionKey(selection)
	a.mu.Lock()
	delete(a.runtimeStops, key)
	a.mu.Unlock()
}

// isRuntimeStopped does not consume the latch: every tab of the env must see it.
func (a *App) isRuntimeStopped(selection uiSelection) bool {
	key := selectionKey(selection)
	a.mu.Lock()
	_, ok := a.runtimeStops[key]
	a.mu.Unlock()
	return ok
}

// runtimeStoppedForSelection trusts the cluster, not the env config; an
// unreadable cluster reads as not stopped.
func (a *App) runtimeStoppedForSelection(selection uiSelection) bool {
	if a.isRuntimeStopped(selection) {
		return true
	}
	_, state, err := a.readRuntimeRunStateForSelection(selection)
	if err != nil {
		return false
	}
	// Ignore ready count: during termination grace it would disagree with
	// `erun open` and let a respawn undo the stop.
	return state.Stopped()
}

// readRuntimeRunStateForSelection is shared so the gate and the Runtime tab
// resolve the same Deployment.
func (a *App) readRuntimeRunStateForSelection(selection uiSelection) (config.OpenResult, lifecycle.RuntimeRunState, error) {
	if a.deps.store == nil || a.deps.readRuntimeRunState == nil {
		return config.OpenResult{}, lifecycle.RuntimeRunState{}, errors.New("the Kubernetes reader is not wired")
	}
	result, err := open.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      strings.TrimSpace(selection.Tenant),
		Environment: strings.TrimSpace(selection.Environment),
	})
	if err != nil {
		return config.OpenResult{}, lifecycle.RuntimeRunState{}, err
	}
	state, err := a.deps.readRuntimeRunState(
		core.Context{},
		lifecycle.RuntimeScaleTargetForResult(result),
	)
	return result, state, err
}

// LoadRuntimeRunState is fail-soft: an unreadable cluster sets Message, which is
// distinct from Present=false.
func (a *App) LoadRuntimeRunState(selection uiSelection) (uiRuntimeRunState, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("load runtime run state", selection.Tenant, selection.Environment); err != nil {
		return uiRuntimeRunState{}, err
	}
	state := uiRuntimeRunState{Tenant: selection.Tenant, Environment: selection.Environment}
	// Check the latch first, or the panel flashes "running" right after a stop.
	if a.isRuntimeStopped(selection) {
		state.Present = true
		state.Stopped = true
		state.RemainingComponents = a.runtimeStopRemainingComponents(selection)
		return state, nil
	}
	resolved, read, err := a.readRuntimeRunStateForSelection(selection)
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
func (a *App) runtimeStopRemainingComponents(selection uiSelection) []string {
	if a.deps.store == nil {
		return nil
	}
	result, err := open.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      strings.TrimSpace(selection.Tenant),
		Environment: strings.TrimSpace(selection.Environment),
	})
	if err != nil {
		return nil
	}
	return lifecycle.StopRemainingComponents(result)
}
