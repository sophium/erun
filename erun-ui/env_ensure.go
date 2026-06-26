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
			// Stamp the dedup window only on success: a failed reconnect must
			// not suppress the next tab's retry for the whole TTL (#644).
			if ensureErr == nil {
				a.envEnsureDone[key] = time.Now()
			}
			a.envEnsureMu.Unlock()
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
	a.emitEnvStatus(selection, envStatusFailed)
	a.emitAppNotification("warn", fmt.Sprintf(
		"Could not reach the runtime for %s/%s: %s. Deploy the environment to bring it up.",
		selection.Tenant, selection.Environment, strings.TrimSpace(err.Error()),
	))
}
