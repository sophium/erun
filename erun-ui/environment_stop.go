package main

import (
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// environment_stop.go is the desktop half of the stop/wake lifecycle. Stopping
// scales the env's runtime Deployment to zero so the node gets its capacity
// back — visible immediately in the Runtime tab's "Available for this runtime"
// figure, which is computed from live pod limits. Waking is deliberately NOT a
// separate desktop path: opening the environment runs `erun open`, which wakes
// it, so there is exactly one wake implementation.

// StopEnvironment stops the selected environment's runtime. The stop is
// recorded on the env config as well as applied to the cluster, so a later
// deploy reconciles it instead of quietly restarting the pod.
func (a *App) StopEnvironment(selection uiSelection) (uiEnvironmentStopResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiEnvironmentStopResult{}, fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uiEnvironmentStopResult{}, err
	}

	// Latch the intent before the scale call, for the same reason the cloud
	// Stop button does: dropping the pod wakes every open tab's reconnect loop,
	// and `erun open` now wakes a stopped env — so without the latch the
	// reconnect would undo the stop before the cluster read could see it.
	a.markRuntimeStopped(selection)
	stopped, err := a.deps.stopEnvironmentRuntime(eruncommon.Context{}, eruncommon.StopEnvironmentParams{
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
		Tenant:         stopped.Tenant,
		Environment:    stopped.Environment,
		Release:        stopped.Release,
		Namespace:      stopped.Namespace,
		AlreadyStopped: stopped.AlreadyStopped,
	}, nil
}

// stopEnvironmentNotice names the recovery action, so the operator is never left
// with a stopped environment and no idea how to get it back — and names the
// sessions the stop ended, so the tabs going dark read as the command finishing
// rather than the environment breaking.
func stopEnvironmentNotice(result eruncommon.StopEnvironmentResult) string {
	if result.AlreadyStopped {
		return fmt.Sprintf("%s/%s was already stopped. Open it to start it again.", result.Tenant, result.Environment)
	}
	sessions := ""
	if len(result.EndedSessions) > 0 {
		sessions = fmt.Sprintf(" %d attached session(s) ended with the pod.", len(result.EndedSessions))
	}
	return fmt.Sprintf("Stopped %s/%s and returned its capacity to the node.%s Open it to start it again.", result.Tenant, result.Environment, sessions)
}

// markRuntimeStopped / clearRuntimeStopped latch a per-env stop the same way the
// cloud-context pair does, but keyed on one env rather than every env sharing a
// cloud context. The latch closes the window the cluster read cannot: dropping
// the pod wakes every open tab's reconnect loop at once, before a `kubectl get`
// could observe the new replica count.
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

// isRuntimeStopped reads the latch without consuming it: an env's ERun and AI
// tabs hit the reconnect gate together when the pod goes away, and each must
// see it.
func (a *App) isRuntimeStopped(selection uiSelection) bool {
	key := selectionKey(selection)
	a.mu.Lock()
	_, ok := a.runtimeStops[key]
	a.mu.Unlock()
	return ok
}

// runtimeStoppedForSelection reports whether the env's runtime Deployment is
// scaled to zero. The cluster is the source of truth for what the UI shows —
// the env config only records what the operator asked for, and an env stopped
// from another machine (or scaled by hand) must still read as stopped here.
// An unreadable cluster is reported as not-stopped: an environment whose state
// cannot be observed is a diagnostic problem, not a stopped environment.
func (a *App) runtimeStoppedForSelection(selection uiSelection) bool {
	if a.isRuntimeStopped(selection) {
		return true
	}
	if a.deps.store == nil || a.deps.readRuntimeRunState == nil {
		return false
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      strings.TrimSpace(selection.Tenant),
		Environment: strings.TrimSpace(selection.Environment),
	})
	if err != nil {
		return false
	}
	state, err := a.deps.readRuntimeRunState(
		eruncommon.Context{},
		eruncommon.RuntimeScaleTargetForResult(result),
	)
	if err != nil {
		return false
	}
	// Stopped is "the Deployment wants no pods", nothing more. Waiting for the
	// ready count to reach zero as well made the gate disagree with the wake for
	// the length of the pod's termination grace: the tabs drop the instant the
	// scale lands, while the old pod is still Ready, so the gate said "running",
	// let the respawn through, and `erun open` — reading the same Deployment —
	// said "stopped, wake it". A stop with a tab open was undone in about a
	// second. "Pods exist but are not ready" with a non-zero desired count is an
	// unhealthy environment and Stopped() already reports false for it.
	return state.Stopped()
}
