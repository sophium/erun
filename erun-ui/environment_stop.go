package main

import (
	"errors"
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
	if err := errMissingTenantOrEnvironment("stop environment", selection.Tenant, selection.Environment); err != nil {
		return uiEnvironmentStopResult{}, err
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
		Tenant:              stopped.Tenant,
		Environment:         stopped.Environment,
		Release:             stopped.Release,
		Namespace:           stopped.Namespace,
		AlreadyStopped:      stopped.AlreadyStopped,
		RemainingComponents: stopped.RemainingComponents,
	}, nil
}

// stopEnvironmentNotice names the recovery action, so the operator is never left
// with a stopped environment and no idea how to get it back — and names the
// sessions the stop ended, so the tabs going dark read as the command finishing
// rather than the environment breaking.
//
// It also names the platform components the stop left running, on both
// outcomes. Those are the pods still visible afterwards, and a pod that
// outlives a stop with no explanation reads as the stop having failed — most of
// all on the already-stopped outcome, where nothing else changed at all.
func stopEnvironmentNotice(result eruncommon.StopEnvironmentResult) string {
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

// stopEnvironmentKeptComponents renders the components the stop left holding
// their capacity, or "" when the environment deploys none: a runtime-only
// environment has nothing to explain and must not gain a sentence saying so.
func stopEnvironmentKeptComponents(result eruncommon.StopEnvironmentResult) string {
	if len(result.RemainingComponents) == 0 {
		return ""
	}
	return fmt.Sprintf(" Its platform component(s) keep running and keep holding capacity: %s.",
		strings.Join(result.RemainingComponents, ", "))
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
	_, state, err := a.readRuntimeRunStateForSelection(selection)
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

// readRuntimeRunStateForSelection resolves one selection to its runtime
// Deployment and reads what the cluster reports about it. The reconnect gate
// and the Runtime tab's own reading share this one resolution so the two can
// never disagree about which Deployment "this environment's runtime" is.
func (a *App) readRuntimeRunStateForSelection(selection uiSelection) (eruncommon.OpenResult, eruncommon.RuntimeRunState, error) {
	if a.deps.store == nil || a.deps.readRuntimeRunState == nil {
		return eruncommon.OpenResult{}, eruncommon.RuntimeRunState{}, errors.New("the Kubernetes reader is not wired")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      strings.TrimSpace(selection.Tenant),
		Environment: strings.TrimSpace(selection.Environment),
	})
	if err != nil {
		return eruncommon.OpenResult{}, eruncommon.RuntimeRunState{}, err
	}
	state, err := a.deps.readRuntimeRunState(
		eruncommon.Context{},
		eruncommon.RuntimeScaleTargetForResult(result),
	)
	return result, state, err
}

// LoadRuntimeRunState reports whether the environment's runtime is running,
// stopped, or not deployed at all — the state the Runtime tab's Stop control
// has to show before it offers the action.
//
// Stop is a real action only when the Deployment wants pods: `erun stop` reads
// exactly these replica counts and reports "already stopped" when it finds
// zero, so a control that never consults them offers a correct no-op as if it
// were the action that frees the node's capacity. The same read also answers
// the other shape of that defect — a runtime that was never deployed, which
// `erun stop` refuses outright — which is why Present is carried separately
// from Stopped rather than folded into a single "nothing to stop" flag.
//
// Fail-soft, like LoadRuntimeUsage: an unreachable cluster yields a Message
// naming what could not be read, never an error that turns the Runtime tab into
// a failure surface. Message is NOT Present=false: "the Deployment is absent"
// and "the Deployment could not be read" are different facts, and the second
// must not render as the first.
func (a *App) LoadRuntimeRunState(selection uiSelection) (uiRuntimeRunState, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("load runtime run state", selection.Tenant, selection.Environment); err != nil {
		return uiRuntimeRunState{}, err
	}
	state := uiRuntimeRunState{Tenant: selection.Tenant, Environment: selection.Environment}
	// The latch is checked first for the same reason the reconnect gate checks
	// it: a stop this desktop just issued drops the pod, and the cluster read
	// cannot see the new replica count until the scale lands — so without this
	// the panel would flash "running" at the operator who just stopped it.
	if a.isRuntimeStopped(selection) {
		state.Present = true
		state.Stopped = true
		state.RemainingComponents = a.runtimeStopRemainingComponents(selection)
		return state, nil
	}
	resolved, read, err := a.readRuntimeRunStateForSelection(selection)
	if err != nil {
		state.Message = "Cannot read this environment's runtime run state: " + err.Error()
		return state, nil
	}
	state.Present = read.Present
	state.DesiredReplicas = read.DesiredReplicas
	state.ReadyReplicas = read.ReadyReplicas
	state.Stopped = read.Stopped()
	state.RemainingComponents = eruncommon.StopRemainingComponents(resolved)
	return state, nil
}

// runtimeStopRemainingComponents answers the same question as the branch above
// for an environment already latched stopped, where nothing was resolved. A
// failed resolve answers with no names rather than an error: the components are
// a qualifier on a stop, not the reason the panel is open.
func (a *App) runtimeStopRemainingComponents(selection uiSelection) []string {
	if a.deps.store == nil {
		return nil
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      strings.TrimSpace(selection.Tenant),
		Environment: strings.TrimSpace(selection.Environment),
	})
	if err != nil {
		return nil
	}
	return eruncommon.StopRemainingComponents(result)
}
