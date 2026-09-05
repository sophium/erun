package main

import (
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// Balances how fast the idle widget and respawn gate see truth
// against AWS describe-instances cost for a user with many contexts.
const cloudContextStatusPollInterval = 10 * time.Second

// cloudContextStatusTTL bounds how long a known-good status stays
// authoritative once AWS stops confirming it. A single failed
// describe-instances call surfaces as Unknown and must not blank a
// known-good status outright — but a sustained run of them must, or a
// context stopped an hour ago keeps reading "running" forever and the
// Stop action keeps getting offered against it. This is the same
// discipline session_heartbeat.go:28-32 applies to session liveness.
const cloudContextStatusTTL = 3 * cloudContextStatusPollInterval

// cloudContextCacheEntry is one context's cached status plus the last time
// that status was actually confirmed by AWS (not merely re-asserted because a
// later Unknown was suppressed).
type cloudContextCacheEntry struct {
	status      string
	confirmedAt time.Time
}

// "" means the poller has not observed this context yet; treat it as
// unknown, never as a real status.
func (a *App) cloudContextStatus(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	a.cloudContextStatusesMu.RLock()
	defer a.cloudContextStatusesMu.RUnlock()
	entry, ok := a.cloudContextStatuses[name]
	if !ok {
		return ""
	}
	if time.Since(entry.confirmedAt) > cloudContextStatusTTL {
		return eruncommon.CloudContextStatusUnknown
	}
	return entry.status
}

// A single transient AWS describe-instances failure surfaces as Unknown, and
// keeping the prior value stops it alone from blanking a known-good status.
// But that grace is bounded by cloudContextStatusTTL: once the last confirmed
// observation is stale, the Unknown downgrade is honoured, so an AWS outage
// that outlasts the grace window can't keep serving a status that may no
// longer be true.
func (a *App) applyCloudContextStatusesToCache(statuses []eruncommon.CloudContextStatus) {
	if len(statuses) == 0 {
		return
	}
	a.cloudContextStatusesMu.Lock()
	defer a.cloudContextStatusesMu.Unlock()
	if a.cloudContextStatuses == nil {
		a.cloudContextStatuses = make(map[string]cloudContextCacheEntry, len(statuses))
	}
	for _, status := range statuses {
		name := strings.TrimSpace(status.Name)
		if name == "" {
			continue
		}
		newStatus := strings.TrimSpace(status.Status)
		if newStatus == "" {
			continue
		}
		if newStatus == eruncommon.CloudContextStatusUnknown {
			if entry, ok := a.cloudContextStatuses[name]; ok && time.Since(entry.confirmedAt) <= cloudContextStatusTTL {
				continue
			}
		}
		a.cloudContextStatuses[name] = cloudContextCacheEntry{status: newStatus, confirmedAt: time.Now()}
	}
}

// Lets Init/Start/Stop handlers reflect the state they just caused
// without waiting for the next poll tick.
func (a *App) setCloudContextStatusInCache(name, status string) {
	if !a.storeCloudContextStatus(name, status) {
		return
	}
	// Outside the write above: the per-env fan-out reads the same cache back,
	// and the sidebar rows for this node are as stale as the widget that just
	// caused the change until it runs.
	a.refreshEnvironmentNodeStatuses()
}

func (a *App) storeCloudContextStatus(name, status string) bool {
	name = strings.TrimSpace(name)
	status = strings.TrimSpace(status)
	if name == "" || status == "" {
		return false
	}
	a.cloudContextStatusesMu.Lock()
	defer a.cloudContextStatusesMu.Unlock()
	if a.cloudContextStatuses == nil {
		a.cloudContextStatuses = make(map[string]cloudContextCacheEntry, 1)
	}
	a.cloudContextStatuses[name] = cloudContextCacheEntry{status: status, confirmedAt: time.Now()}
	return true
}

func (a *App) startCloudContextStatusPoller() {
	a.mu.Lock()
	if a.cloudContextPollerStop != nil {
		a.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	a.cloudContextPollerStop = stop
	a.mu.Unlock()
	go a.runCloudContextStatusPoller(stop)
}

func (a *App) stopCloudContextStatusPoller() {
	a.mu.Lock()
	stop := a.cloudContextPollerStop
	a.cloudContextPollerStop = nil
	a.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (a *App) runCloudContextStatusPoller(stop <-chan struct{}) {
	a.refreshCloudContextStatusesOnce()
	ticker := time.NewTicker(cloudContextStatusPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.refreshCloudContextStatusesOnce()
		}
	}
}

func (a *App) refreshCloudContextStatusesOnce() {
	if a.deps.store == nil {
		return
	}
	statuses, err := eruncommon.RefreshCloudContextStatuses(eruncommon.Context{}, a.deps.store, a.deps.cloudContextDeps)
	if err != nil {
		return
	}
	a.applyCloudContextStatusesToCache(statuses)
	// A cached node status nothing publishes per environment is a status the
	// sidebar cannot render, which is how an environment on a stopped node came
	// to show no indicator at all.
	a.refreshEnvironmentNodeStatuses()
}
