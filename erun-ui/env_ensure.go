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

// ensureEnvRuntimeOnce reconnects the env's MCP/API port-forwards at most once
// per (re)start window, across every tab and respawn (issue #463). Since `open`
// became a pure primitive (issue #644) this is a thin idempotent reconnect: it
// runs `erun open --no-shell` only to (re)bind the forwarders against the
// already-deployed runtime — it does NOT deploy. Deploy is the caller's job:
// the desktop composes build→push→deploy on create and via the Deploy button,
// so the runtime is expected to already exist by the time tabs open.
//
// Fire-and-forget by design: callers never block on the reconnect, and a
// concurrent or recent successful reconnect is not repeated. A FAILED reconnect
// (usually because the runtime is not deployed) is surfaced — not swallowed —
// and does NOT stamp the success window, so the next tab open retries instead
// of being suppressed for the whole TTL while the user stares at a dead env
// (issue #644 proposed change 3).
func (a *App) ensureEnvRuntimeOnce(selection uiSelection) {
	// a.ctx is set by startup() in both desktop and headless modes and stays
	// nil in unit tests: the ensure must never fall back from a test app to
	// the machine's real CLI and config (the #492 hazard class).
	if a.ctx == nil {
		return
	}
	selection = normalizeSelection(selection)
	key := selectionKey(selection)

	a.envEnsureMu.Lock()
	if a.envEnsureInflight == nil {
		a.envEnsureInflight = make(map[string]struct{})
		a.envEnsureDone = make(map[string]time.Time)
		a.envEnsureFailNotified = make(map[string]struct{})
	}
	if _, inflight := a.envEnsureInflight[key]; inflight {
		a.envEnsureMu.Unlock()
		return
	}
	if done, ok := a.envEnsureDone[key]; ok && time.Since(done) < envEnsureTTL {
		a.envEnsureMu.Unlock()
		return
	}
	a.envEnsureInflight[key] = struct{}{}
	a.envEnsureMu.Unlock()

	go func() {
		var ensureErr error
		defer func() {
			a.envEnsureMu.Lock()
			delete(a.envEnsureInflight, key)
			reached := ensureErr == nil
			// Stamp the dedup window only on success: a failed reconnect must
			// not suppress the next tab's retry for the whole TTL (#644).
			if reached {
				a.envEnsureDone[key] = time.Now()
				// Reached again — end this failure episode so a later failure
				// re-surfaces its notification (#711).
				delete(a.envEnsureFailNotified, key)
			}
			a.envEnsureMu.Unlock()
			// Runtime reached — the "Could not reach the runtime …" warning, if
			// one is still up, is now stale; clear it (#713). Emitted outside
			// the mutex; the frontend only clears a matching notification.
			if reached {
				a.emitClearEnvNotification(selection.Tenant, selection.Environment, notificationSourceRuntimeUnreachable)
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
}

// surfaceEnvRuntimeEnsureFailure makes a failed runtime reconnect visible and
// recoverable instead of discarding it (Nielsen #1/#9, issue #644): it flags
// the env's sidebar row as failed and posts an actionable notification. The
// reconnect usually fails because the runtime is not deployed — which `open`
// no longer fixes on its own — so the recovery is an explicit deploy.
func (a *App) surfaceEnvRuntimeEnsureFailure(selection uiSelection, err error) {
	selection = normalizeSelection(selection)
	// A deploy for this env being in flight IS the recovery this failure would
	// recommend ("Deploy the environment …"), and the deploy-progress overlay
	// already communicates it. Surfacing a contradictory failed status + banner
	// on top of the running deploy is the #713 confusion, so stay quiet while the
	// deploy owns the env's state; a genuine post-deploy failure surfaces afresh.
	if a.deployInFlightForEnv(selection) {
		return
	}
	// The sidebar row's failed status is the persistent signal and is updated on
	// every attempt. The notification is transient and posts only once per
	// failure episode: the ensure retries on every tab open/respawn (it does not
	// stamp the success TTL on failure, #644), so re-posting on each retry made
	// the banner re-appear the instant the user dismissed it (#711). The dedup is
	// cleared when the env is reached again, so a later failure surfaces afresh.
	a.emitEnvStatus(selection, envStatusFailed)
	key := selectionKey(selection)
	a.envEnsureMu.Lock()
	if a.envEnsureFailNotified == nil {
		a.envEnsureFailNotified = make(map[string]struct{})
	}
	_, alreadyNotified := a.envEnsureFailNotified[key]
	a.envEnsureFailNotified[key] = struct{}{}
	a.envEnsureMu.Unlock()
	if alreadyNotified {
		return
	}
	// Tag the notification with the env + a stable source so the deploy
	// lifecycle can clear it once the state it describes moves on (#713).
	a.emitEnvNotification("warn", selection.Tenant, selection.Environment, notificationSourceRuntimeUnreachable, fmt.Sprintf(
		"Could not reach the runtime for %s/%s: %s. Deploy the environment to bring it up.",
		selection.Tenant, selection.Environment, strings.TrimSpace(err.Error()),
	))
}

// deployInFlightForEnv reports whether a deploy that locks terminals is
// currently in flight for the env: any of its open sessions is locked by an
// activity (only deploys lock sibling terminals — see sessionMatchesActivity).
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
