package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/kubecheck"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
)

// envEnsureTTL lets one reconnect cover the burst of spawns an env (re)start produces.
const envEnsureTTL = 30 * time.Second

// envEnsureReason: a tab spawn defers to a recent success; a forward repair or a
// failed edge probe has evidence that outranks it.
type envEnsureReason int

const (
	envEnsureForTabSpawn envEnsureReason = iota
	envEnsureForBrokenForward
)

// ensureEnvRuntimeOnce rebinds MCP/API forwarders at most once per (re)start window. It never deploys.
func (a *App) ensureEnvRuntimeOnce(selection uiSelection) {
	a.ensureEnvRuntime(selection, envEnsureForTabSpawn)
}

// envEnsureRun is one rebind in flight; reached is written before settled closes, so waiters read it lock-free.
type envEnsureRun struct {
	settled chan struct{}
	reached bool
}

// claimEnvRuntimeEnsure takes the per-env latch. In-flight runs block every caller;
// the completed window only covers callers without evidence of a fault. A refused
// caller gets the running run back; mine reports whether this call performs it.
func (a *App) claimEnvRuntimeEnsure(key string, reason envEnsureReason) (run *envEnsureRun, mine bool) {
	a.envEnsureMu.Lock()
	defer a.envEnsureMu.Unlock()
	if a.envEnsureInflight == nil {
		a.envEnsureInflight = make(map[string]*envEnsureRun)
		a.envEnsureDone = make(map[string]time.Time)
		a.envEnsureFailNotified = make(map[string]struct{})
	}
	if inflight, ok := a.envEnsureInflight[key]; ok {
		return inflight, false
	}
	if done, ok := a.envEnsureDone[key]; ok && reason == envEnsureForTabSpawn && time.Since(done) < envEnsureTTL {
		return nil, false
	}
	run = &envEnsureRun{settled: make(chan struct{})}
	a.envEnsureInflight[key] = run
	return run, true
}

// ensureEnvRuntime starts a rebind and reports whether this call started it. A failed
// reconnect is surfaced and does not stamp the success window, so the next open retries.
func (a *App) ensureEnvRuntime(selection uiSelection, reason envEnsureReason) bool {
	selection, run, mine := a.beginEnvRuntimeEnsure(selection, reason)
	if !mine {
		return false
	}
	go a.runEnvRuntimeEnsure(selection, selectionKey(selection), run)
	return true
}

// ensureEnvRuntimeWithin waits up to budget for the rebind, for callers that next hand
// a client this port. Timing out is not a verdict; the rebind keeps running.
func (a *App) ensureEnvRuntimeWithin(selection uiSelection, reason envEnsureReason, budget time.Duration) bool {
	selection, run, mine := a.beginEnvRuntimeEnsure(selection, reason)
	if run == nil {
		return false
	}
	if mine {
		go a.runEnvRuntimeEnsure(selection, selectionKey(selection), run)
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-run.settled:
		return run.reached
	case <-timer.C:
		return false
	}
}

// beginEnvRuntimeEnsure returns a nil run when the window already confirmed reach or
// a.ctx is nil (unit tests), so tests never fall back to the real CLI.
func (a *App) beginEnvRuntimeEnsure(selection uiSelection, reason envEnsureReason) (uiSelection, *envEnsureRun, bool) {
	if a.ctx == nil {
		return selection, nil, false
	}
	selection = normalizeSelection(selection)
	run, mine := a.claimEnvRuntimeEnsure(selectionKey(selection), reason)
	return selection, run, mine
}

// runEnvRuntimeEnsure is the run's only writer, so settling needs no extra lock.
func (a *App) runEnvRuntimeEnsure(selection uiSelection, key string, run *envEnsureRun) {
	var ensureErr error
	defer func() {
		a.envEnsureMu.Lock()
		delete(a.envEnsureInflight, key)
		reached := ensureErr == nil
		if reached {
			a.envEnsureDone[key] = time.Now()
			// End the failure episode so a later failure notifies again.
			delete(a.envEnsureFailNotified, key)
		}
		run.reached = reached
		close(run.settled)
		a.envEnsureMu.Unlock()
		if reached {
			a.emitClearEnvNotification(selection.Tenant, selection.Environment, "")
		}
	}()
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		ensureErr = err
		a.surfaceEnvRuntimeEnsureFailure(selection, err)
		return
	}
	onLine := newActivityTraceLineHandler(a, selection, sessionKindOpen)
	if ensureErr = a.deps.reconnectMCP(context.Background(), result, onLine); ensureErr != nil {
		a.surfaceEnvRuntimeEnsureFailure(selection, ensureErr)
	}
}

// surfaceEnvRuntimeEnsureFailure reports a failed reconnect. A failure does not mean
// the runtime is down; only a confirmed absent deployment offers the deploy action.
func (a *App) surfaceEnvRuntimeEnsureFailure(selection uiSelection, err error) {
	selection = normalizeSelection(selection)
	// An in-flight deploy is already the recovery, and its overlay says so.
	if a.deployInFlightForEnv(selection) {
		return
	}
	// `open --reconnect` refuses to start a stopped runtime; that is not an outage.
	if a.runtimeStoppedForSelection(selection) {
		a.surfaceEnvRuntimeStopped(selection)
		return
	}
	// The row status updates every attempt; the notification posts once per failure episode.
	a.emitEnvStatus(selection, envStatusFailed)
	if a.ensureFailureAlreadyNotified(selection) {
		return
	}
	// Kind "warning" (not "warn") is what the frontend maps to the attention icon.
	if runtimeCheckFoundDeploymentAbsent(err) {
		a.emitEnvNotification("warning", selection.Tenant, selection.Environment, notificationSourceRuntimeUnreachable, fmt.Sprintf(
			"Could not reach the runtime for %s/%s: %s. Deploy the environment to bring it up.",
			selection.Tenant, selection.Environment, strings.TrimSpace(err.Error()),
		), notificationActionDeploy)
		return
	}
	// Absence is not confirmed, so no deploy is offered; it could roll a healthy runtime.
	a.emitEnvNotification("warning", selection.Tenant, selection.Environment, notificationSourceRuntimeUnreachable, fmt.Sprintf(
		"Could not tell whether the runtime for %s/%s is up: %s. Check the cluster connection, credentials, and permissions for this environment.",
		selection.Tenant, selection.Environment, strings.TrimSpace(err.Error()),
	), "")
}

// runtimeCheckFoundDeploymentAbsent matches only ensureRuntimeDeployed's
// "runtime is not deployed" error; any other failure is not evidence of absence.
func runtimeCheckFoundDeploymentAbsent(err error) bool {
	return err != nil && strings.Contains(err.Error(), kubecheck.KubernetesDeploymentAbsentMessageMarker)
}

// surfaceEnvRuntimeStopped shows a stopped env as stopped and names the way back.
func (a *App) surfaceEnvRuntimeStopped(selection uiSelection) {
	a.emitEnvStatus(selection, envStatusRuntimeStopped)
	if a.ensureFailureAlreadyNotified(selection) {
		return
	}
	a.emitEnvNotification("info", selection.Tenant, selection.Environment, notificationSourceRuntimeUnreachable, fmt.Sprintf(
		"%s/%s is stopped, so its sessions did not reconnect. Open it to start it again.",
		selection.Tenant, selection.Environment,
	), "")
}

// ensureFailureAlreadyNotified latches one notification per failure episode.
func (a *App) ensureFailureAlreadyNotified(selection uiSelection) bool {
	key := selectionKey(selection)
	a.envEnsureMu.Lock()
	defer a.envEnsureMu.Unlock()
	if a.envEnsureFailNotified == nil {
		a.envEnsureFailNotified = make(map[string]struct{})
	}
	_, alreadyNotified := a.envEnsureFailNotified[key]
	a.envEnsureFailNotified[key] = struct{}{}
	return alreadyNotified
}

// deployInFlightForEnv: only deploys lock sibling terminals, so a locked session is the signal.
func (a *App) deployInFlightForEnv(selection uiSelection) bool {
	selection = normalizeSelection(selection)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, managed := range a.sessions {
		if managed == nil || managed.closed || managed.lockedByActivity == "" {
			continue
		}
		if strings.TrimSpace(managed.selection.Tenant) == selection.Tenant &&
			strings.TrimSpace(managed.selection.Environment) == selection.Environment {
			return true
		}
	}
	return false
}
