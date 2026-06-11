package main

import (
	"context"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// envEnsureTTL bounds how long one completed ensure stands in for the next
// tab spawn or respawn of the same env. Long enough to cover the burst an
// env (re)start produces (the ERun + AI spawns and any pod-replace respawns
// land within seconds of each other), short enough that a genuinely later
// reopen re-checks the deployment.
const envEnsureTTL = 30 * time.Second

// ensureEnvRuntimeOnce runs the env's open/build/deploy preflight at most
// once per (re)start window, across every tab and respawn (issue #463).
// Before this, each tab's own `erun open` ran the full preflight — spec
// resolution, per-arch docker cache walks, and, when stale, the whole
// multi-arch rebuild + helm deploy — once per tab, with the rebuild
// streaming into whichever tab won the per-env queue (the reported "docker
// build inside the AI tab"). Now the desktop runs one `erun open --no-shell`
// per window, streams its deploy traces into the activity queue (the same
// `==> Deploying` parser the PTY channel feeds), and every tab launches with
// --skip-ensure, relying on the shell runner's deployment wait to hold the
// tab until this ensure's deploy is available.
//
// Fire-and-forget by design: callers never block on the ensure, and a
// concurrent or recent ensure is simply not repeated. A failed ensure still
// stamps the window (the TTL stops a failing env from being hammered once
// per tab); its failure surfaces through the activity queue's deploy-failed
// entry and the tabs' own deployment-wait errors, which feed the existing
// reconnect-refusal machinery.
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
		defer func() {
			a.envEnsureMu.Lock()
			delete(a.envEnsureInflight, key)
			a.envEnsureDone[key] = time.Now()
			a.envEnsureMu.Unlock()
		}()
		result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
			Tenant:      selection.Tenant,
			Environment: selection.Environment,
		})
		if err != nil {
			return
		}
		onLine := newActivityTraceLineHandler(a, selection, sessionKindOpen)
		_ = a.deps.reconnectMCP(context.Background(), result, onLine)
	}()
}
