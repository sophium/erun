package cloudctx

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/cloud/contextpower"
)

// Balances status freshness against describe-instances cost for many contexts.
const pollInterval = 10 * time.Second

// StatusTTL bounds how long a known-good status survives repeated Unknowns.
const StatusTTL = 3 * pollInterval

// cacheEntry records when AWS last actually confirmed the status.
type cacheEntry struct {
	status      string
	confirmedAt time.Time
}

// Status is the cached power state of a cloud context; "" means not observed
// yet, which callers treat as unknown.
func (s *Service) Status(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	entry, ok := s.statuses[name]
	if !ok {
		return ""
	}
	if s.now().Sub(entry.confirmedAt) > StatusTTL {
		return cloudcontext.CloudContextStatusUnknown
	}
	return entry.status
}

// ApplyStatuses records a refresh. A transient Unknown keeps the prior value,
// but only until StatusTTL expires.
func (s *Service) ApplyStatuses(statuses []model.CloudContextStatus) {
	if len(statuses) == 0 {
		return
	}
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	for _, status := range statuses {
		name := strings.TrimSpace(status.Name)
		if name == "" {
			continue
		}
		newStatus := strings.TrimSpace(status.Status)
		if newStatus == "" {
			continue
		}
		if newStatus == cloudcontext.CloudContextStatusUnknown {
			if entry, ok := s.statuses[name]; ok && s.now().Sub(entry.confirmedAt) <= StatusTTL {
				continue
			}
		}
		s.statuses[name] = cacheEntry{status: newStatus, confirmedAt: s.now()}
	}
}

// SetStatus records a status a start or stop handler just caused.
func (s *Service) SetStatus(name, status string) {
	if !s.storeStatus(name, status) {
		return
	}
	// Outside the write above: the per-env fan-out reads the same cache back.
	s.d.OnStatusChange()
}

func (s *Service) storeStatus(name, status string) bool {
	name = strings.TrimSpace(name)
	status = strings.TrimSpace(status)
	if name == "" || status == "" {
		return false
	}
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.statuses[name] = cacheEntry{status: status, confirmedAt: s.now()}
	return true
}

// Start runs the status poller; a second call while it runs is a no-op.
func (s *Service) Start() {
	s.mu.Lock()
	if s.pollerStop != nil {
		s.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	s.pollerStop = stop
	s.mu.Unlock()
	go s.runPoller(stop)
}

// Stop ends the status poller.
func (s *Service) Stop() {
	s.mu.Lock()
	stop := s.pollerStop
	s.pollerStop = nil
	s.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (s *Service) runPoller(stop <-chan struct{}) {
	s.refreshOnce()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.refreshOnce()
		}
	}
}

func (s *Service) refreshOnce() {
	statuses, err := contextpower.RefreshCloudContextStatuses(core.Context{}, s.d.Store, s.d.ContextDeps)
	if err != nil {
		return
	}
	s.ApplyStatuses(statuses)
	s.d.OnStatusChange()
}
