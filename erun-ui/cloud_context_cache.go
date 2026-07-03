package main

import (
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// Balances how fast the idle widget and respawn gate see truth
// against AWS describe-instances cost for a user with many contexts.
const cloudContextStatusPollInterval = 10 * time.Second

// "" means the poller has not observed this context yet; treat it as
// unknown, never as a real status.
func (a *App) cloudContextStatus(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	a.cloudContextStatusesMu.RLock()
	defer a.cloudContextStatusesMu.RUnlock()
	return a.cloudContextStatuses[name]
}

// A transient AWS describe-instances failure surfaces as Unknown;
// keeping the prior value stops it from blanking a known-good status.
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

// Lets Init/Start/Stop handlers reflect the state they just caused
// without waiting for the next poll tick.
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
}
