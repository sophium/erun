package main

import "time"

// environment_usage.go answers the comparison question environment_activity.go
// cannot: not just whether an environment is busy, but how much of its own
// CPU/memory/disk it is using — the evidence an operator needs to compare
// environments against each other at a glance, not only against themselves the
// way the Runtime tab's on-demand reading already does. It reuses LoadRuntimeUsage
// unchanged (the same kubectl-exec probe the Runtime tab's refresh button
// drives), but calls it from a bounded sweep instead of per-hover: hovering an
// environment must never trigger a probe of its own (the same precedent the activity poller set).

// environmentUsageInterval paces the usage sweep. It is slower than
// environmentActivityInterval (20s, environment_activity.go) because each
// reading costs a real kubectl exec plus a 1s in-container CPU sample, not a
// lightweight local HTTP call — sampling every configured environment that
// often would multiply real exec load across the fleet for a reading that does
// not need second-by-second freshness to let an operator compare environments.
const environmentUsageInterval = 90 * time.Second

// environmentUsageReading pairs one cached usage reading with when it was
// taken, so a consumer can render both the figures and their age rather than
// presenting a cached number as if it were live.
type environmentUsageReading struct {
	usage      uiRuntimeUsage
	observedAt time.Time
}

func (a *App) runEnvironmentUsagePoller(stop <-chan struct{}) {
	// No eager first sweep, matching runEnvironmentActivityPoller: boot is
	// already the desktop's busiest moment, and a reading that arrives one tick
	// late costs nothing a hover card cannot already show as "not yet observed".
	ticker := time.NewTicker(environmentUsageInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.reconcileEnvironmentUsageOnce()
		}
	}
}

// reconcileEnvironmentUsageOnce samples every configured environment, gated on
// the activity sweep's own reachability observation: an environment with no
// confirmed live pod gets an explicit "not running" reading rather than a
// spent kubectl exec that would only fail the same way the activity sweep
// already knows it would.
func (a *App) reconcileEnvironmentUsageOnce() {
	reachability := a.envActivitySnapshot()
	for _, selection := range a.configuredSelections() {
		a.sampleEnvironmentUsageOnce(selection, reachability)
	}
}

func (a *App) sampleEnvironmentUsageOnce(selection uiSelection, reachability map[string]environmentActivityState) {
	key := selectionKey(selection)
	state, known := reachability[key]
	var usage uiRuntimeUsage
	switch {
	case !known || !state.reachable:
		// Reported so, never as a bare 0: a stopped environment, a host
		// environment (never reachable by this check — there is no pod to
		// forward to), and one nobody has opened here all land in this branch,
		// and none of them are "idle at 0%".
		usage = uiRuntimeUsage{
			Tenant:      selection.Tenant,
			Environment: selection.Environment,
			Message:     "Not running, or not open here: there is no runtime pod to measure.",
		}
	case a.deps.loadRuntimeUsage != nil:
		read, err := a.deps.loadRuntimeUsage(a.backgroundContext(), selection)
		if err != nil {
			usage = uiRuntimeUsage{
				Tenant:      selection.Tenant,
				Environment: selection.Environment,
				Message:     "Cannot read this environment's resource usage: " + err.Error(),
			}
		} else {
			usage = read
		}
	default:
		return
	}
	reading := environmentUsageReading{usage: usage, observedAt: time.Now()}
	a.mu.Lock()
	if a.envUsage == nil {
		a.envUsage = make(map[string]environmentUsageReading)
	}
	a.envUsage[key] = reading
	a.mu.Unlock()
	a.persistEnvironmentUsage(selection, reading)
	a.emitEnvUsage(selection, reading)
}

// envUsageSnapshot copies the poller's per-environment readings under lock, for
// callers assembling a read model outside any a.mu section of their own —
// mirrors envActivitySnapshot.
func (a *App) envUsageSnapshot() map[string]environmentUsageReading {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.envUsage) == 0 {
		return nil
	}
	out := make(map[string]environmentUsageReading, len(a.envUsage))
	for k, v := range a.envUsage {
		out[k] = v
	}
	return out
}

// seedEnvironmentUsageSnapshots attaches each environment's last cached
// reading, if any, to the initial-state read model — the same reason
// seedEnvironmentActivitySnapshots exists: a Redux reset that does not restart
// the Go process must not wait out a full sweep interval before a hover card
// has anything to show. a.envUsage itself is now seeded from
// environment_usage_history.go's persisted file at App construction, so this
// also covers a genuine process restart, not only a Redux reset: the reading
// it attaches renders stale with its true age rather than as "Not yet
// observed" for an environment this desktop measured moments before exiting.
func (a *App) seedEnvironmentUsageSnapshots(state *uiState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for ti := range state.Tenants {
		tenant := &state.Tenants[ti]
		for ei := range tenant.Environments {
			env := &tenant.Environments[ei]
			key := selectionKey(uiSelection{Tenant: tenant.Name, Environment: env.Name})
			reading, ok := a.envUsage[key]
			if !ok {
				continue
			}
			env.Usage = uiEnvironmentUsageSnapshotFrom(reading)
		}
	}
}

func (a *App) emitEnvUsage(selection uiSelection, reading environmentUsageReading) {
	snapshot := uiEnvironmentUsageSnapshotFrom(reading)
	a.emitEvent(envUsageEvent, envUsagePayload{
		Tenant:            selection.Tenant,
		Environment:       selection.Environment,
		Usage:             snapshot.Usage,
		ObservedAtUnix:    snapshot.ObservedAtUnix,
		StaleAfterSeconds: snapshot.StaleAfterSeconds,
	})
}

// uiEnvironmentUsageSnapshotFrom carries StaleAfterSeconds alongside every
// reading so a renderer never has to hardcode environmentUsageInterval to
// decide whether a figure has gone stale.
func uiEnvironmentUsageSnapshotFrom(reading environmentUsageReading) *uiEnvironmentUsageSnapshot {
	return &uiEnvironmentUsageSnapshot{
		Usage:             reading.usage,
		ObservedAtUnix:    reading.observedAt.Unix(),
		StaleAfterSeconds: int64(environmentUsageInterval / time.Second),
	}
}
