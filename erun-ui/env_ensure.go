package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// envEnsureTTL bounds how long one completed reconnect stands in for the next
// tab spawn or respawn of the same env. Long enough to cover the burst an
// env (re)start produces (the ERun + AI spawns and any pod-replace respawns
// land within seconds of each other), short enough that a genuinely later
// reopen re-checks reachability.
const envEnsureTTL = 30 * time.Second

// envEnsureReason says why a rebind is being asked for, because the two callers
// hold different evidence. A tab spawn is one of a burst and has no reason to
// think anything is wrong, so a recent success stands in for it. A forward
// repair has watched the forward stop carrying traffic *since* that success, so
// the window it stamped is exactly the thing that must not suppress it.
type envEnsureReason int

const (
	envEnsureForTabSpawn envEnsureReason = iota
	envEnsureForBrokenForward
)

// ensureEnvRuntimeOnce rebinds the env's MCP/API forwarders against the
// already-deployed runtime, at most once per (re)start window across every tab
// and respawn. It does NOT deploy — deploy is the caller's job — so an
// undeployed env stays down here rather than being brought up.
func (a *App) ensureEnvRuntimeOnce(selection uiSelection) {
	a.ensureEnvRuntime(selection, envEnsureForTabSpawn)
}

// claimEnvRuntimeEnsure takes the per-env rebind latch and reports whether this
// caller got it. The in-flight half applies to every caller — it is what keeps
// one rebind per environment at a time — while the completed window only stands
// in for a caller with no evidence that anything is wrong.
func (a *App) claimEnvRuntimeEnsure(key string, reason envEnsureReason) bool {
	a.envEnsureMu.Lock()
	defer a.envEnsureMu.Unlock()
	if a.envEnsureInflight == nil {
		a.envEnsureInflight = make(map[string]struct{})
		a.envEnsureDone = make(map[string]time.Time)
		a.envEnsureFailNotified = make(map[string]struct{})
	}
	if _, inflight := a.envEnsureInflight[key]; inflight {
		return false
	}
	if done, ok := a.envEnsureDone[key]; ok && reason == envEnsureForTabSpawn && time.Since(done) < envEnsureTTL {
		return false
	}
	a.envEnsureInflight[key] = struct{}{}
	return true
}

// ensureEnvRuntime is the rebind itself, and reports whether this call is the
// one that started it. Both dedup gates stay in force for every reason: the
// in-flight latch is what guarantees one rebind per env at a time, so a repair
// can never race a spawn for the same local port.
//
// Fire-and-forget: a failed reconnect is surfaced, not swallowed, and does not
// stamp the success window, so the next tab open retries instead of being
// suppressed for the whole TTL while the user stares at a dead env.
func (a *App) ensureEnvRuntime(selection uiSelection, reason envEnsureReason) bool {
	// a.ctx is set by startup() in both desktop and headless modes and stays
	// nil in unit tests: the ensure must never fall back from a test app to
	// the machine's real CLI and config.
	if a.ctx == nil {
		return false
	}
	selection = normalizeSelection(selection)
	key := selectionKey(selection)
	if !a.claimEnvRuntimeEnsure(key, reason) {
		return false
	}

	go func() {
		var ensureErr error
		defer func() {
			a.envEnsureMu.Lock()
			delete(a.envEnsureInflight, key)
			reached := ensureErr == nil
			// Stamp the dedup window only on success: a failed reconnect must
			// not suppress the next tab's retry for the whole TTL.
			if reached {
				a.envEnsureDone[key] = time.Now()
				// Reached again — end this failure episode so a later failure
				// re-surfaces its notification.
				delete(a.envEnsureFailNotified, key)
			}
			a.envEnsureMu.Unlock()
			// Runtime reached — any prior "could not reach the runtime" or
			// deploy-failed warning for this env is now stale; clear it.
			if reached {
				a.emitClearEnvNotification(selection.Tenant, selection.Environment, "")
			}
		}()
		result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
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
	}()
	return true
}

// surfaceEnvRuntimeEnsureFailure makes a failed runtime reconnect visible and
// recoverable instead of discarding it (Nielsen #1/#9). The reconnect usually
// fails because the runtime is not deployed — which `open` no longer fixes on
// its own — so the recovery it points to is an explicit deploy.
func (a *App) surfaceEnvRuntimeEnsureFailure(selection uiSelection, err error) {
	selection = normalizeSelection(selection)
	// A deploy for this env being in flight IS the recovery this failure would
	// recommend ("Deploy the environment …"), and the deploy-progress overlay
	// already communicates it. Surfacing a contradictory failed status + banner
	// on top of the running deploy is confusing, so stay quiet while the
	// deploy owns the env's state; a genuine post-deploy failure surfaces afresh.
	if a.deployInFlightForEnv(selection) {
		return
	}
	// A stopped environment is not an unreachable one. The rebind runs `open
	// --reconnect`, which refuses to start a stopped runtime by design, so
	// reporting that refusal as a failure would render the operator's own Stop
	// as an outage and offer a deploy that is not the recovery.
	if a.runtimeStoppedForSelection(selection) {
		a.surfaceEnvRuntimeStopped(selection)
		return
	}
	// The sidebar row's failed status is the persistent signal and is updated on
	// every attempt. The notification is transient and posts only once per
	// failure episode: the ensure retries on every tab open/respawn (it does not
	// stamp the success TTL on failure), so re-posting on each retry made
	// the banner re-appear the instant the user dismissed it. The dedup is
	// cleared when the env is reached again, so a later failure surfaces afresh.
	a.emitEnvStatus(selection, envStatusFailed)
	if a.ensureFailureAlreadyNotified(selection) {
		return
	}
	// Tag the notification with the env + a stable source so the deploy
	// lifecycle can clear it once the state it describes moves on. Kind
	// "warning" (not "warn") is the contract the frontend maps to the
	// attention icon; an unrecognized kind renders as a neutral info ⓘ.
	a.emitEnvNotification("warning", selection.Tenant, selection.Environment, notificationSourceRuntimeUnreachable, fmt.Sprintf(
		"Could not reach the runtime for %s/%s: %s. Deploy the environment to bring it up.",
		selection.Tenant, selection.Environment, strings.TrimSpace(err.Error()),
	), notificationActionDeploy)
}

// surfaceEnvRuntimeStopped renders a stopped environment as stopped and names
// the way back, so the row is recoverable by recognition rather than by the
// operator recalling that opening is what starts one (Nielsen #1, #6, #9).
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

// ensureFailureAlreadyNotified latches one notification per failure episode and
// reports whether this attempt is a repeat. The latch is cleared when the env is
// reached again, so a later episode surfaces afresh.
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

// deployInFlightForEnv reports whether a terminal-locking deploy is currently
// in flight for the env. Only deploys lock sibling terminals (see
// sessionMatchesActivity), so a locked open session for this env is the signal.
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
