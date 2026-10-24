package main

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// Balances status freshness against describe-instances cost for many contexts.
const cloudContextStatusPollInterval = 10 * time.Second

// cloudContextStatusTTL bounds how long a known-good status survives repeated Unknowns.
const cloudContextStatusTTL = 3 * cloudContextStatusPollInterval

// cloudContextCacheEntry records when AWS last actually confirmed the status.
type cloudContextCacheEntry struct {
	status      string
	confirmedAt time.Time
}

// "" means not observed yet; treat it as unknown.
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
		return cloud.CloudContextStatusUnknown
	}
	return entry.status
}

// A transient Unknown keeps the prior value, but only until cloudContextStatusTTL expires.
func (a *App) applyCloudContextStatusesToCache(statuses []model.CloudContextStatus) {
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
		if newStatus == cloud.CloudContextStatusUnknown {
			if entry, ok := a.cloudContextStatuses[name]; ok && time.Since(entry.confirmedAt) <= cloudContextStatusTTL {
				continue
			}
		}
		a.cloudContextStatuses[name] = cloudContextCacheEntry{status: newStatus, confirmedAt: time.Now()}
	}
}

func (a *App) setCloudContextStatusInCache(name, status string) {
	if !a.storeCloudContextStatus(name, status) {
		return
	}
	// Outside the write above: the per-env fan-out reads the same cache back.
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
	statuses, err := cloud.RefreshCloudContextStatuses(core.Context{}, a.deps.store, a.deps.cloudContextDeps)
	if err != nil {
		return
	}
	a.applyCloudContextStatusesToCache(statuses)
	a.refreshEnvironmentNodeStatuses()
}
