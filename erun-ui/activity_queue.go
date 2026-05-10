package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// activityQueueStatus is the lifecycle phase of a tracked deploy. The desktop
// surfaces these states distinctly so the user can tell waiting/running/done
// apart at a glance.
type activityQueueStatus string

const (
	activityQueueStatusRunning   activityQueueStatus = "running"
	activityQueueStatusSucceeded activityQueueStatus = "succeeded"
	activityQueueStatusFailed    activityQueueStatus = "failed"
	activityQueueStatusSkipped   activityQueueStatus = "skipped"
)

// activityQueueContainerStatus is one container's last observed state under the
// deploy's release. The frontend renders these as per-container pills.
type activityQueueContainerStatus struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	Phase    string `json:"phase"`
	Ready    bool   `json:"ready"`
	Restarts int    `json:"restarts"`
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
}

// activityQueueEntry is a single tracked long-running command. It survives
// across desktop restarts via on-disk persistence so a long rollout the
// user kicked off before closing the app remains visible when they
// reopen.
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
	// MarkerPath records where the on-disk RunningCommand marker for this
	// entry lives. Empty for entries that originated outside the marker
	// watcher (e.g. legacy in-memory state). Populated by the watcher so
	// it can detect marker disappearance and finalize the entry.
	MarkerPath string `json:"-"`
}

// activityQueueStore keeps active and recent deploy entries. Callers mutate
// state through start/update/finish/dismiss; reads return cloned snapshots so
// callers can pass them to Wails event emitters without races.
type activityQueueStore struct {
	mu      sync.Mutex
	active  map[string]*activityQueueEntry
	history []*activityQueueEntry
	persist func([]*activityQueueEntry) error
	now     func() time.Time
	notify  func(activityQueueEntry)
}

// activityQueueHistoryCapacity caps the in-memory + on-disk history length so
// the persisted file stays small even after many deploys.
const activityQueueHistoryCapacity = 50

// activityQueueRunningStaleAfter is the threshold past which a "running" entry
// loaded from disk is reconciled to "failed". The desktop process owning the
// deploy died without writing a terminal status; the user shouldn't see a
// permanent ghost-running card.
const activityQueueRunningStaleAfter = 30 * time.Minute

func newActivityQueueStore(persist func([]*activityQueueEntry) error, notify func(activityQueueEntry), now func() time.Time) *activityQueueStore {
	if now == nil {
		now = time.Now
	}
	return &activityQueueStore{
		active:  make(map[string]*activityQueueEntry),
		persist: persist,
		notify:  notify,
		now:     now,
	}
}

// list returns a chronological snapshot (newest first) of every tracked
// deploy across active + history.
func (s *activityQueueStore) list() []activityQueueEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// findActive returns the first active entry matching (tenant, environment).
// Used by lock-on-deploy logic that targets all activities for a selection.
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

// findActiveByCommand returns the active entry for (command, tenant,
// environment). Used by the deploy-button gate so a same-version deploy
// can be detected independently of an unrelated build for the same env.
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

// start registers a new active activity. If an entry with the same ID
// already exists, the existing entry is returned so callers (the marker
// watcher and explicit registration paths) can collapse into the same
// record without duplicates.
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
	s.flushLocked()
	s.notifyLocked(snapshot)
	return snapshot, true
}

// updateContainers merges a new container-status snapshot into the entry. No
// effect if the entry has already finished.
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
	s.flushLocked()
	s.notifyLocked(snapshot)
}

// finish moves an active entry into history with the given terminal status.
// Returns false if the entry was already finished (idempotent in the face of
// duplicate ==> Deploy failed / ==> Deployed lines from the PTY tail).
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
	now := s.now().UTC()
	entry.EndedAt = &now
	entry.LastUpdated = now
	delete(s.active, id)
	s.history = append([]*activityQueueEntry{entry}, s.history...)
	if len(s.history) > activityQueueHistoryCapacity {
		s.history = s.history[:activityQueueHistoryCapacity]
	}
	snapshot := *cloneActivityQueueEntry(entry)
	s.flushLocked()
	s.notifyLocked(snapshot)
	return snapshot, true
}

// dismiss removes a finished entry from history. Active entries are never
// dismissed — the user must wait for the deploy to reach a terminal state.
func (s *activityQueueStore) dismiss(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, entry := range s.history {
		if entry.ID == id {
			s.history = append(s.history[:i], s.history[i+1:]...)
			s.flushLocked()
			return true
		}
	}
	return false
}

// load replaces the in-memory state with the supplied snapshot. Stale running
// entries (older than activityQueueRunningStaleAfter) are reconciled to
// failed because the process that owned them is no longer alive.
func (s *activityQueueStore) load(entries []*activityQueueEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = make(map[string]*activityQueueEntry)
	s.history = nil
	cutoff := s.now().UTC().Add(-activityQueueRunningStaleAfter)
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		clone := cloneActivityQueueEntry(entry)
		if clone.Status == activityQueueStatusRunning && clone.LastUpdated.Before(cutoff) {
			clone.Status = activityQueueStatusFailed
			ended := s.now().UTC()
			clone.EndedAt = &ended
			clone.LastUpdated = ended
			if clone.Error == "" {
				clone.Error = "deploy state lost (desktop restart before completion)"
			}
		}
		if clone.Status == activityQueueStatusRunning {
			s.active[clone.ID] = clone
		} else {
			s.history = append(s.history, clone)
		}
	}
	sort.SliceStable(s.history, func(i, j int) bool {
		return s.history[i].StartedAt.After(s.history[j].StartedAt)
	})
	if len(s.history) > activityQueueHistoryCapacity {
		s.history = s.history[:activityQueueHistoryCapacity]
	}
	s.flushLocked()
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

func (s *activityQueueStore) flushLocked() {
	if s.persist == nil {
		return
	}
	snapshot := make([]*activityQueueEntry, 0, len(s.active)+len(s.history))
	for _, entry := range s.active {
		snapshot = append(snapshot, cloneActivityQueueEntry(entry))
	}
	for _, entry := range s.history {
		snapshot = append(snapshot, cloneActivityQueueEntry(entry))
	}
	sort.SliceStable(snapshot, func(i, j int) bool {
		return snapshot[i].StartedAt.After(snapshot[j].StartedAt)
	})
	_ = s.persist(snapshot)
}

func (s *activityQueueStore) notifyLocked(snapshot activityQueueEntry) {
	if s.notify == nil {
		return
	}
	go s.notify(snapshot)
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

func activityQueueStatePath(stateDir string) string {
	return filepath.Join(stateDir, "deploy_queue.json")
}

// defaultActivityQueueStatePath returns the platform user-config path the
// desktop persists deploy-queue state to. Returns "" when the OS doesn't
// expose a config dir; callers treat that as "skip persistence".
func defaultActivityQueueStatePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", "deploy_queue.json")
}

// loadActivityQueueStateFromDisk reads the persisted snapshot at path. Missing
// file is not an error — the desktop returns an empty list and starts fresh.
// Malformed JSON is logged via the caller's logger and treated as empty so a
// corrupt file can never block the desktop from starting.
func loadActivityQueueStateFromDisk(path string) ([]*activityQueueEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var entries []*activityQueueEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// writeActivityQueueStateAtomic writes the snapshot under a temp file then
// renames so a crash mid-write cannot truncate the persisted state.
func writeActivityQueueStateAtomic(path string, entries []*activityQueueEntry) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "deploy_queue-*.tmp")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp.Name())
		}
	}()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(entries); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
