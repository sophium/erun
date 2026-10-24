package main

import "time"

// Usage is sampled by a bounded sweep (via LoadRuntimeUsage), never per hover.

// environmentUsageInterval is slow because each reading is a kubectl exec plus a
// 1s in-container CPU sample.
const environmentUsageInterval = 90 * time.Second

// environmentUsageReading keeps the sample time so cached figures render with their age.
type environmentUsageReading struct {
	usage      uiRuntimeUsage
	observedAt time.Time
}

func (a *App) runEnvironmentUsagePoller(stop <-chan struct{}) {
	// No eager first sweep: boot is the desktop's busiest moment.
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

// reconcileEnvironmentUsageOnce skips the exec for environments the activity
// sweep found unreachable.
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
		// Never a bare 0: an unreachable environment is not "idle at 0%".
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

// envUsageSnapshot locks a.mu (not reentrant); callers holding it read a.envUsage directly.
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

// ResetEnvironmentUsageObservations clears only the in-memory cache; the
// persisted file is read once, at App construction.
func (a *App) ResetEnvironmentUsageObservations() {
	a.mu.Lock()
	a.envUsage = nil
	a.mu.Unlock()
}

// seedEnvironmentUsageSnapshots attaches the last cached reading, persisted
// across restarts, so a hover card shows it with its true age.
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

// StaleAfterSeconds spares renderers from hardcoding environmentUsageInterval.
func uiEnvironmentUsageSnapshotFrom(reading environmentUsageReading) *uiEnvironmentUsageSnapshot {
	return &uiEnvironmentUsageSnapshot{
		Usage:             reading.usage,
		ObservedAtUnix:    reading.observedAt.Unix(),
		StaleAfterSeconds: int64(environmentUsageInterval / time.Second),
	}
}
