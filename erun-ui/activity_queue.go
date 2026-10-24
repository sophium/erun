package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type activityQueueStatus string

const (
	activityQueueStatusWaiting   activityQueueStatus = "waiting"
	activityQueueStatusRunning   activityQueueStatus = "running"
	activityQueueStatusSucceeded activityQueueStatus = "succeeded"
	activityQueueStatusFailed    activityQueueStatus = "failed"
	activityQueueStatusSkipped   activityQueueStatus = "skipped"
	activityQueueStatusCancelled activityQueueStatus = "cancelled"
)

func activityQueueStatusIsTerminal(s activityQueueStatus) bool {
	switch s {
	case activityQueueStatusSucceeded,
		activityQueueStatusFailed,
		activityQueueStatusSkipped,
		activityQueueStatusCancelled:
		return true
	case activityQueueStatusWaiting, activityQueueStatusRunning:
		fallthrough
	default:
		return false
	}
}

type activityQueueContainerStatus struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	Phase    string `json:"phase"`
	Ready    bool   `json:"ready"`
	Restarts int    `json:"restarts"`
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
}

// activityQueueEntry is a tracked long-running command, rebuilt each launch
// from helm releases and live PTY sessions, never persisted.
type activityQueueEntry struct {
	ID                string                         `json:"id"`
	Command           string                         `json:"command"`
	Tenant            string                         `json:"tenant"`
	Environment       string                         `json:"environment"`
	Version           string                         `json:"version,omitempty"`
	Release           string                         `json:"release,omitempty"`
	Namespace         string                         `json:"namespace,omitempty"`
	KubernetesContext string                         `json:"kubernetesContext,omitempty"`
	Component         string                         `json:"component,omitempty"`
	Image             string                         `json:"image,omitempty"`
	Summary           string                         `json:"summary,omitempty"`
	Status            activityQueueStatus            `json:"status"`
	StartedAt         time.Time                      `json:"startedAt"`
	EndedAt           *time.Time                     `json:"endedAt,omitempty"`
	LastUpdated       time.Time                      `json:"lastUpdated"`
	Containers        []activityQueueContainerStatus `json:"containers,omitempty"`
	Error             string                         `json:"error,omitempty"`
	// Detail is the captured output behind a failed entry.
	Detail string `json:"detail,omitempty"`
	// Source is "helm", "shell", "trace", or "action"; it selects lifecycle rules.
	Source    string `json:"source,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	// ActionKind labels the user action behind the entry; empty for observational entries.
	ActionKind string `json:"actionKind,omitempty"`
	// EnqueuedAt is set only on action-source entries.
	EnqueuedAt       *time.Time `json:"enqueuedAt,omitempty"`
	StartedRunningAt *time.Time `json:"startedRunningAt,omitempty"`
}

// activityQueueStore returns cloned snapshots for race-free emission. A single
// drain goroutine delivers notifications in the order the store applied them.
type activityQueueStore struct {
	mu       sync.Mutex
	active   map[string]*activityQueueEntry
	history  []*activityQueueEntry
	now      func() time.Time
	notify   func(activityQueueEntry)
	notifyCh chan activityQueueEntry
	// outputByID buffers recent output per active entry; finish copies the tail
	// into Detail on failure. It never crosses the wire.
	outputByID map[string][]string
}

const activityQueueHistoryCapacity = 50

// activityQueueNotifyBuffer sits well above expected burst depth so normal use never drops.
const activityQueueNotifyBuffer = 256

// activityQueueOutputBufferLines keeps the tail, which holds the actual error.
const activityQueueOutputBufferLines = 200

// activityQueueOutputLineMaxChars clips pathological lines.
const activityQueueOutputLineMaxChars = 2000

func newActivityQueueStore(notify func(activityQueueEntry), now func() time.Time) *activityQueueStore {
	if now == nil {
		now = time.Now
	}
	s := &activityQueueStore{
		active:     make(map[string]*activityQueueEntry),
		outputByID: make(map[string][]string),
		now:        now,
		notify:     notify,
		notifyCh:   make(chan activityQueueEntry, activityQueueNotifyBuffer),
	}
	go s.runNotifyLoop()
	return s
}

// runNotifyLoop drains even with a nil notify so the buffer never fills.
func (s *activityQueueStore) runNotifyLoop() {
	for snapshot := range s.notifyCh {
		if s.notify != nil {
			s.notify(snapshot)
		}
	}
}

func (s *activityQueueStore) list() []activityQueueEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *activityQueueStore) findActive(tenant, environment string) (activityQueueEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.active {
		if entry.Tenant == tenant && entry.Environment == environment {
			return *cloneActivityQueueEntry(entry), true
		}
	}
	return activityQueueEntry{}, false
}

// findActiveByCommand also keys on command so a deploy is not confused with a build for the same env.
func (s *activityQueueStore) findActiveByCommand(command, tenant, environment string) (activityQueueEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.active {
		if entry.Command == command && entry.Tenant == tenant && entry.Environment == environment {
			return *cloneActivityQueueEntry(entry), true
		}
	}
	return activityQueueEntry{}, false
}

// latestDeployFailed reports whether the env's latest deploy failed; reconnect
// uses it to stop retrying an env whose pod will never come ready.
func (s *activityQueueStore) latestDeployFailed(tenant, environment string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.snapshotLocked() {
		if entry.Command != "deploy" || entry.Tenant != tenant || entry.Environment != environment {
			continue
		}
		return entry.Status == activityQueueStatusFailed
	}
	return false
}

// promoteToRunning returns false for an already-running entry.
func (s *activityQueueStore) promoteToRunning(id string) (activityQueueEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.active[id]
	if !ok {
		return activityQueueEntry{}, false
	}
	if entry.Status != activityQueueStatusWaiting {
		return *cloneActivityQueueEntry(entry), false
	}
	entry.Status = activityQueueStatusRunning
	now := s.now().UTC()
	entry.StartedRunningAt = &now
	entry.LastUpdated = now
	snapshot := *cloneActivityQueueEntry(entry)
	s.notifyLocked(snapshot)
	return snapshot, true
}

// start returns the existing entry for a duplicate ID so pollers and explicit
// registration converge on one record.
func (s *activityQueueStore) start(seed activityQueueEntry) (activityQueueEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seed.ID == "" {
		seed.ID = generateActivityQueueID(seed)
	}
	if existing, ok := s.active[seed.ID]; ok {
		return *cloneActivityQueueEntry(existing), false
	}
	if seed.Status == "" {
		seed.Status = activityQueueStatusRunning
	}
	now := s.now().UTC()
	if seed.StartedAt.IsZero() {
		seed.StartedAt = now
	}
	seed.LastUpdated = now
	entry := seed
	s.active[entry.ID] = &entry
	snapshot := *cloneActivityQueueEntry(&entry)
	s.notifyLocked(snapshot)
	return snapshot, true
}

func (s *activityQueueStore) updateContainers(id string, containers []activityQueueContainerStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.active[id]
	if !ok {
		return
	}
	entry.Containers = append(entry.Containers[:0:0], containers...)
	entry.LastUpdated = s.now().UTC()
	snapshot := *cloneActivityQueueEntry(entry)
	s.notifyLocked(snapshot)
}

// recordOutputLine buffers output into every active entry for the tenant/env,
// since a line cannot be attributed to one command; no-op when none is active.
func (s *activityQueueStore) recordOutputLine(tenant, environment, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(line) > activityQueueOutputLineMaxChars {
		line = line[:activityQueueOutputLineMaxChars]
	}
	for _, entry := range s.active {
		if entry.Tenant != tenant || entry.Environment != environment {
			continue
		}
		buf := s.outputByID[entry.ID]
		buf = append(buf, line)
		if len(buf) > activityQueueOutputBufferLines {
			buf = buf[len(buf)-activityQueueOutputBufferLines:]
		}
		s.outputByID[entry.ID] = buf
	}
}

// finish is idempotent to absorb duplicate deploy trace lines.
func (s *activityQueueStore) finish(id string, status activityQueueStatus, errMsg string) (activityQueueEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.active[id]
	if !ok {
		return activityQueueEntry{}, false
	}
	entry.Status = status
	if errMsg != "" {
		entry.Error = errMsg
	}
	if status == activityQueueStatusFailed {
		if lines := s.outputByID[id]; len(lines) > 0 {
			entry.Detail = strings.Join(lines, "\n")
		}
	}
	delete(s.outputByID, id)
	now := s.now().UTC()
	entry.EndedAt = &now
	entry.LastUpdated = now
	delete(s.active, id)
	s.history = append([]*activityQueueEntry{entry}, s.history...)
	if len(s.history) > activityQueueHistoryCapacity {
		s.history = s.history[:activityQueueHistoryCapacity]
	}
	snapshot := *cloneActivityQueueEntry(entry)
	s.notifyLocked(snapshot)
	return snapshot, true
}

// dismiss removes only finished entries; stuck active ones need forceDismiss.
func (s *activityQueueStore) dismiss(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, entry := range s.history {
		if entry.ID == id {
			s.history = append(s.history[:i], s.history[i+1:]...)
			return true
		}
	}
	return false
}

// forceDismiss removes an entry regardless of status and returns it for follow-up recovery.
func (s *activityQueueStore) forceDismiss(id string) (entry activityQueueEntry, removedFromActive bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, found := s.active[id]; found {
		entry = *cloneActivityQueueEntry(existing)
		delete(s.active, id)
		delete(s.outputByID, id)
		return entry, true, true
	}
	for i, candidate := range s.history {
		if candidate.ID == id {
			entry = *cloneActivityQueueEntry(candidate)
			s.history = append(s.history[:i], s.history[i+1:]...)
			return entry, false, true
		}
	}
	return activityQueueEntry{}, false, false
}

func (s *activityQueueStore) snapshotLocked() []activityQueueEntry {
	out := make([]activityQueueEntry, 0, len(s.active)+len(s.history))
	for _, entry := range s.active {
		out = append(out, *cloneActivityQueueEntry(entry))
	}
	for _, entry := range s.history {
		out = append(out, *cloneActivityQueueEntry(entry))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

// notifyLocked requires s.mu. A full buffer drops the snapshot; re-emits and
// periodic re-snapshots recover it.
func (s *activityQueueStore) notifyLocked(snapshot activityQueueEntry) {
	if s.notify == nil {
		return
	}
	select {
	case s.notifyCh <- snapshot:
	default:
	}
}

func cloneActivityQueueEntry(e *activityQueueEntry) *activityQueueEntry {
	if e == nil {
		return nil
	}
	out := *e
	if e.Containers != nil {
		out.Containers = append([]activityQueueContainerStatus(nil), e.Containers...)
	}
	if e.EndedAt != nil {
		end := *e.EndedAt
		out.EndedAt = &end
	}
	return &out
}

func generateActivityQueueID(e activityQueueEntry) string {
	return fmt.Sprintf("%s/%s/%s/%s@%d", sanitizeActivityQueueIDPart(e.Command), sanitizeActivityQueueIDPart(e.Tenant), sanitizeActivityQueueIDPart(e.Environment), sanitizeActivityQueueIDPart(e.Version), e.StartedAt.UnixNano())
}

func sanitizeActivityQueueIDPart(s string) string {
	if s == "" {
		return "_"
	}
	return s
}
