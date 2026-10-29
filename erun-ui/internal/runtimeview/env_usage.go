package runtimeview

import (
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Usage is sampled by a bounded sweep (via LoadRuntimeUsage), never per hover.

// environmentUsageInterval is slow because each reading is a kubectl exec plus a
// 1s in-container CPU sample.
const environmentUsageInterval = 90 * time.Second

// UsageReading keeps the sample time so cached figures render with their age.
type UsageReading struct {
	Usage      Usage
	ObservedAt time.Time
}

// RunUsagePoller sweeps until stop closes.
func (s *Service) RunUsagePoller(stop <-chan struct{}) {
	// No eager first sweep: boot is the desktop's busiest moment.
	ticker := time.NewTicker(environmentUsageInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.reconcileEnvironmentUsageOnce()
		}
	}
}

// reconcileEnvironmentUsageOnce skips the exec for environments the activity
// sweep found unreachable.
func (s *Service) reconcileEnvironmentUsageOnce() {
	reachability := s.ActivitySnapshot()
	for _, selection := range s.configuredSelections() {
		s.sampleEnvironmentUsageOnce(selection, reachability)
	}
}

func (s *Service) sampleEnvironmentUsageOnce(selection uimodel.Selection, reachability map[string]ActivityState) {
	key := uimodel.SelectionKey(selection)
	state, known := reachability[key]
	var usage Usage
	if !known || !state.Reachable {
		// Never a bare 0: an unreachable environment is not "idle at 0%".
		usage = Usage{
			Tenant:      selection.Tenant,
			Environment: selection.Environment,
			Message:     "Not running, or not open here: there is no runtime pod to measure.",
		}
	} else if read, err := s.d.LoadUsage(s.d.Context(), selection); err != nil {
		usage = Usage{
			Tenant:      selection.Tenant,
			Environment: selection.Environment,
			Message:     "Cannot read this environment's resource usage: " + err.Error(),
		}
	} else {
		usage = read
	}
	s.PublishUsage(selection, UsageReading{Usage: usage, ObservedAt: time.Now()})
}

// PublishUsage caches, persists and emits a reading.
func (s *Service) PublishUsage(selection uimodel.Selection, reading UsageReading) {
	s.mu.Lock()
	if s.usage == nil {
		s.usage = make(map[string]UsageReading)
	}
	s.usage[uimodel.SelectionKey(selection)] = reading
	s.mu.Unlock()
	s.persistEnvironmentUsage(selection, reading)
	s.emitEnvUsage(selection, reading)
}

// UsageSnapshot copies the last reading per selection key, nil when none.
func (s *Service) UsageSnapshot() map[string]UsageReading {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.usage) == 0 {
		return nil
	}
	out := make(map[string]UsageReading, len(s.usage))
	for k, v := range s.usage {
		out[k] = v
	}
	return out
}

// ResetEnvironmentUsageObservations clears only the in-memory cache; the
// persisted file is read once, at construction.
func (s *Service) ResetEnvironmentUsageObservations() {
	s.mu.Lock()
	s.usage = nil
	s.mu.Unlock()
}

func (s *Service) emitEnvUsage(selection uimodel.Selection, reading UsageReading) {
	snapshot := UsageSnapshotFrom(reading)
	s.d.Emit(uimodel.EnvUsageEvent, UsagePayload{
		Tenant:            selection.Tenant,
		Environment:       selection.Environment,
		Usage:             snapshot.Usage,
		ObservedAtUnix:    snapshot.ObservedAtUnix,
		StaleAfterSeconds: snapshot.StaleAfterSeconds,
	})
}

// UsageSnapshotFrom sets StaleAfterSeconds so renderers need not hardcode the sweep interval.
func UsageSnapshotFrom(reading UsageReading) *UsageSnapshot {
	return &UsageSnapshot{
		Usage:             reading.Usage,
		ObservedAtUnix:    reading.ObservedAt.Unix(),
		StaleAfterSeconds: int64(environmentUsageInterval / time.Second),
	}
}
