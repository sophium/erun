package main

import (
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// cloudContextStatusPollInterval governs how often the background
// poller calls AWS describe-instances to refresh the cache. Short
// enough that the titlebar idle widget and the respawn gate converge
// to truth within a single user attention span, long enough that a
// user with a dozen contexts does not pay an AWS describe-instances
// call every second.
const cloudContextStatusPollInterval = 10 * time.Second

// cloudContextStatus returns the cached AWS-observed Status for the
// named context, or "" when nothing has been observed yet. Readers
// must treat "" as "unknown" — the cache is populated lazily and the
// first observation only lands once the poller has fired.
func (a *App) cloudContextStatus(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	a.cloudContextStatusesMu.RLock()
	defer a.cloudContextStatusesMu.RUnlock()
	return a.cloudContextStatuses[name]
}

// applyCloudContextStatusesToCache merges the supplied refresh results
// into the in-memory cache. Authoritative observations (running,
// pending, stopped) overwrite the previous value. Unknown is treated
// as "no new authoritative observation" and leaves the previous entry
// in place, so a transient AWS describe-instances failure does not
// blank a known-good status; an explicit Unknown stays cached only
// when the slot was empty.
func (a *App) applyCloudContextStatusesToCache(statuses []eruncommon.CloudContextStatus) {
	if len(statuses) == 0 {
		return
	}
	a.cloudContextStatusesMu.Lock()
	defer a.cloudContextStatusesMu.Unlock()
	if a.cloudContextStatuses == nil {
		a.cloudContextStatuses = make(map[string]string, len(statuses))
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
			if _, ok := a.cloudContextStatuses[name]; ok {
				continue
			}
		}
		a.cloudContextStatuses[name] = newStatus
	}
}

// setCloudContextStatusInCache writes a single observation to the
// cache. Used by Init/Start/Stop handlers, which already know the
// intended new state and should not have to wait for the next poll
// tick before linkedCloudContext sees the change.
func (a *App) setCloudContextStatusInCache(name, status string) {
	name = strings.TrimSpace(name)
	status = strings.TrimSpace(status)
	if name == "" || status == "" {
		return
	}
	a.cloudContextStatusesMu.Lock()
	defer a.cloudContextStatusesMu.Unlock()
	if a.cloudContextStatuses == nil {
		a.cloudContextStatuses = make(map[string]string, 1)
	}
	a.cloudContextStatuses[name] = status
}

// startCloudContextStatusPoller launches the background reconciler
// that keeps the cache aligned with AWS. Started from App.startup.
// Idempotent: a second call while the poller is running is a no-op.
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

// stopCloudContextStatusPoller signals the poller to exit.
// Idempotent: safe to call multiple times during shutdown.
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
}
