package main

import (
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// activityPollerInterval is short enough that deploy state transitions
// surface within seconds, yet long enough to keep kubectl/helm load negligible.
const activityPollerInterval = 5 * time.Second

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
	// The stale detector reconciles sessions whose process died; the heartbeat
	// poller reconciles the opposite case — a session that is alive but has
	// stopped printing. Together they keep what the UI shows tied to what the
	// pod is doing rather than to stream traffic.
	go a.runSessionHeartbeatPoller(stop)
	// Both of the above only see environments the desktop itself opened. This
	// one sweeps every configured environment, so one driven from the CLI or by
	// an in-pod agent stops rendering as untouched.
	go a.runEnvironmentActivityPoller(stop)
}

// stopActivityPollers is idempotent: safe to call more than once.
func (a *App) stopActivityPollers() {
	a.mu.Lock()
	stop := a.activityPollersStop
	a.activityPollersStop = nil
	a.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

// seedConfiguredKubeContexts surfaces deploys left pending by a previous run
// before any session opens. Store errors are deliberately non-fatal — startup
// falls back to on-demand context discovery.
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

func (a *App) snapshotConfiguredKubeContexts() []string {
	a.activityWatchedContextsMu.Lock()
	defer a.activityWatchedContextsMu.Unlock()
	out := make([]string, 0, len(a.activityWatchedContexts))
	for name := range a.activityWatchedContexts {
		out = append(out, name)
	}
	return out
}

// watchedKubeContexts bounds the helm poller to contexts actually in use, so
// helm list stays cheap for users with large kubeconfigs.
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
