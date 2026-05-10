package main

import (
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// activityPollerInterval governs the cadence for both the helm-release
// poller and the stale-shell detector. The interval is short enough that
// users see deploy state transitions within a few seconds while keeping
// kubectl/helm load negligible.
const activityPollerInterval = 5 * time.Second

// startActivityPollers launches the background reconcilers that keep the
// activity queue in sync with real cluster + host state. Started from
// App.startup once the Wails context is available so that subprocesses
// can be cancelled cleanly on shutdown.
//
// On startup the helm poller also seeds its watch set from the user's
// configured environments (every env's KubernetesContext) so a deploy
// stuck pending from a previous desktop run is surfaced immediately,
// without waiting for the user to first open a session in that
// context.
func (a *App) startActivityPollers() {
	a.mu.Lock()
	if a.activityPollersStop != nil {
		a.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	a.activityPollersStop = stop
	a.mu.Unlock()

	a.seedConfiguredKubeContexts()

	go a.runHelmReleasePoller(stop)
	go a.runStaleShellDetector(stop)
}

// stopActivityPollers signals both pollers to exit. Idempotent: safe to
// call multiple times during shutdown teardown.
func (a *App) stopActivityPollers() {
	a.mu.Lock()
	stop := a.activityPollersStop
	a.activityPollersStop = nil
	a.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

// seedConfiguredKubeContexts walks the user's configured envs and adds
// their KubernetesContext values to the watch set so the helm poller
// covers them even before a session has been opened. Errors from the
// store are swallowed — startup continues with the empty bootstrap set
// and contexts are added on demand from open sessions.
func (a *App) seedConfiguredKubeContexts() {
	if a.deps.store == nil {
		return
	}
	result, err := eruncommon.ResolveListResult(a.deps.store, a.deps.findProjectRoot, eruncommon.OpenParams{})
	if err != nil {
		return
	}
	a.activityWatchedContextsMu.Lock()
	defer a.activityWatchedContextsMu.Unlock()
	if a.activityWatchedContexts == nil {
		a.activityWatchedContexts = make(map[string]struct{})
	}
	for _, tenant := range result.Tenants {
		for _, env := range tenant.Environments {
			if name := strings.TrimSpace(env.KubernetesContext); name != "" {
				a.activityWatchedContexts[name] = struct{}{}
			}
		}
	}
}

// rememberKubeContextForActivity adds a kube context to the watch set
// at runtime, e.g. when a new session is started or an activity entry
// is registered with a previously-unseen context.
func (a *App) rememberKubeContextForActivity(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	a.activityWatchedContextsMu.Lock()
	defer a.activityWatchedContextsMu.Unlock()
	if a.activityWatchedContexts == nil {
		a.activityWatchedContexts = make(map[string]struct{})
	}
	a.activityWatchedContexts[name] = struct{}{}
}

// snapshotConfiguredKubeContexts returns a stable copy of the watch set
// so callers can iterate without holding the mutex.
func (a *App) snapshotConfiguredKubeContexts() []string {
	a.activityWatchedContextsMu.Lock()
	defer a.activityWatchedContextsMu.Unlock()
	out := make([]string, 0, len(a.activityWatchedContexts))
	for name := range a.activityWatchedContexts {
		out = append(out, name)
	}
	return out
}

// watchedKubeContexts returns the set of kube contexts the helm poller
// should query on this tick. Contexts come from four sources:
//
//   - The user's configured environments (seeded once on startup via
//     seedConfiguredKubeContexts), so previously-pending deploys are
//     surfaced before the user opens a session.
//   - Active activity entries (deploys already known to the queue).
//   - Open managed terminal sessions whose selection carries a
//     non-empty KubernetesContext.
//   - Contexts remembered at runtime via rememberKubeContextForActivity
//     when a new session/entry references one not yet in the set.
//
// Limiting the watch set keeps `helm list` calls bounded — a user with
// 30 contexts in their kubeconfig only pays for the ones they're
// actually using.
func (a *App) watchedKubeContexts() []string {
	seen := make(map[string]struct{})
	var contexts []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		contexts = append(contexts, name)
	}
	for _, name := range a.snapshotConfiguredKubeContexts() {
		add(name)
	}
	if a.activityQueue != nil {
		for _, entry := range a.activityQueue.list() {
			if entry.Status == activityQueueStatusRunning {
				add(entry.KubernetesContext)
			}
		}
	}
	a.mu.Lock()
	for _, managed := range a.sessions {
		if managed == nil || managed.closed {
			continue
		}
		add(managed.selection.KubernetesContext)
	}
	a.mu.Unlock()
	return contexts
}
