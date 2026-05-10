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

// deployQueueStatus is the lifecycle phase of a tracked deploy. The desktop
// surfaces these states distinctly so the user can tell waiting/running/done
// apart at a glance.
type deployQueueStatus string

const (
	deployQueueStatusRunning   deployQueueStatus = "running"
	deployQueueStatusSucceeded deployQueueStatus = "succeeded"
	deployQueueStatusFailed    deployQueueStatus = "failed"
	deployQueueStatusSkipped   deployQueueStatus = "skipped"
)

// deployQueueContainerStatus is one container's last observed state under the
// deploy's release. The frontend renders these as per-container pills.
type deployQueueContainerStatus struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	Phase    string `json:"phase"`
	Ready    bool   `json:"ready"`
	Restarts int    `json:"restarts"`
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
}

// deployQueueEntry is a single tracked deploy attempt. It survives across
// desktop restarts via on-disk persistence so a long rollout the user kicked
// off before closing the app remains visible when they reopen.
type deployQueueEntry struct {
	ID                string                       `json:"id"`
	Tenant            string                       `json:"tenant"`
	Environment       string                       `json:"environment"`
	Version           string                       `json:"version,omitempty"`
	Release           string                       `json:"release"`
	Namespace         string                       `json:"namespace"`
	KubernetesContext string                       `json:"kubernetesContext"`
	Status            deployQueueStatus            `json:"status"`
	StartedAt         time.Time                    `json:"startedAt"`
	EndedAt           *time.Time                   `json:"endedAt,omitempty"`
	LastUpdated       time.Time                    `json:"lastUpdated"`
	Containers        []deployQueueContainerStatus `json:"containers,omitempty"`
	Error             string                       `json:"error,omitempty"`
}

// deployQueueStore keeps active and recent deploy entries. Callers mutate
// state through start/update/finish/dismiss; reads return cloned snapshots so
// callers can pass them to Wails event emitters without races.
type deployQueueStore struct {
	mu      sync.Mutex
	active  map[string]*deployQueueEntry
	history []*deployQueueEntry
	persist func([]*deployQueueEntry) error
	now     func() time.Time
	notify  func(deployQueueEntry)
}

// deployQueueHistoryCapacity caps the in-memory + on-disk history length so
// the persisted file stays small even after many deploys.
const deployQueueHistoryCapacity = 50

// deployQueueRunningStaleAfter is the threshold past which a "running" entry
// loaded from disk is reconciled to "failed". The desktop process owning the
// deploy died without writing a terminal status; the user shouldn't see a
// permanent ghost-running card.
const deployQueueRunningStaleAfter = 30 * time.Minute

func newDeployQueueStore(persist func([]*deployQueueEntry) error, notify func(deployQueueEntry), now func() time.Time) *deployQueueStore {
	if now == nil {
		now = time.Now
	}
	return &deployQueueStore{
		active:  make(map[string]*deployQueueEntry),
		persist: persist,
		notify:  notify,
		now:     now,
	}
}

// list returns a chronological snapshot (newest first) of every tracked
// deploy across active + history.
func (s *deployQueueStore) list() []deployQueueEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// findActive returns the active entry for (tenant, environment) if any. Used
// by the UI to gate the deploy button when an in-flight deploy already
// targets the same selection.
func (s *deployQueueStore) findActive(tenant, environment string) (deployQueueEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.active {
		if entry.Tenant == tenant && entry.Environment == environment {
			return *cloneDeployQueueEntry(entry), true
		}
	}
	return deployQueueEntry{}, false
}

// start registers a new active deploy. If an active entry already exists for
// the same (tenant, environment, version), the existing entry is returned so
// the caller can join it instead of adding a duplicate.
func (s *deployQueueStore) start(seed deployQueueEntry) (deployQueueEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.active {
		if entry.Tenant == seed.Tenant && entry.Environment == seed.Environment && entry.Version == seed.Version {
			return *cloneDeployQueueEntry(entry), false
		}
	}
	if seed.ID == "" {
		seed.ID = generateDeployQueueID(seed)
	}
	if seed.Status == "" {
		seed.Status = deployQueueStatusRunning
	}
	now := s.now().UTC()
	if seed.StartedAt.IsZero() {
		seed.StartedAt = now
	}
	seed.LastUpdated = now
	entry := seed
	s.active[entry.ID] = &entry
	snapshot := *cloneDeployQueueEntry(&entry)
	s.flushLocked()
	s.notifyLocked(snapshot)
	return snapshot, true
}

// updateContainers merges a new container-status snapshot into the entry. No
// effect if the entry has already finished.
func (s *deployQueueStore) updateContainers(id string, containers []deployQueueContainerStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.active[id]
	if !ok {
		return
	}
	entry.Containers = append(entry.Containers[:0:0], containers...)
	entry.LastUpdated = s.now().UTC()
	snapshot := *cloneDeployQueueEntry(entry)
	s.flushLocked()
	s.notifyLocked(snapshot)
}

// finish moves an active entry into history with the given terminal status.
// Returns false if the entry was already finished (idempotent in the face of
// duplicate ==> Deploy failed / ==> Deployed lines from the PTY tail).
func (s *deployQueueStore) finish(id string, status deployQueueStatus, errMsg string) (deployQueueEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.active[id]
	if !ok {
		return deployQueueEntry{}, false
	}
	entry.Status = status
	if errMsg != "" {
		entry.Error = errMsg
	}
	now := s.now().UTC()
	entry.EndedAt = &now
	entry.LastUpdated = now
	delete(s.active, id)
	s.history = append([]*deployQueueEntry{entry}, s.history...)
	if len(s.history) > deployQueueHistoryCapacity {
		s.history = s.history[:deployQueueHistoryCapacity]
	}
	snapshot := *cloneDeployQueueEntry(entry)
	s.flushLocked()
	s.notifyLocked(snapshot)
	return snapshot, true
}

// dismiss removes a finished entry from history. Active entries are never
// dismissed — the user must wait for the deploy to reach a terminal state.
func (s *deployQueueStore) dismiss(id string) bool {
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
// entries (older than deployQueueRunningStaleAfter) are reconciled to
// failed because the process that owned them is no longer alive.
func (s *deployQueueStore) load(entries []*deployQueueEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = make(map[string]*deployQueueEntry)
	s.history = nil
	cutoff := s.now().UTC().Add(-deployQueueRunningStaleAfter)
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		clone := cloneDeployQueueEntry(entry)
		if clone.Status == deployQueueStatusRunning && clone.LastUpdated.Before(cutoff) {
			clone.Status = deployQueueStatusFailed
			ended := s.now().UTC()
			clone.EndedAt = &ended
			clone.LastUpdated = ended
			if clone.Error == "" {
				clone.Error = "deploy state lost (desktop restart before completion)"
			}
		}
		if clone.Status == deployQueueStatusRunning {
			s.active[clone.ID] = clone
		} else {
			s.history = append(s.history, clone)
		}
	}
	sort.SliceStable(s.history, func(i, j int) bool {
		return s.history[i].StartedAt.After(s.history[j].StartedAt)
	})
	if len(s.history) > deployQueueHistoryCapacity {
		s.history = s.history[:deployQueueHistoryCapacity]
	}
	s.flushLocked()
}

func (s *deployQueueStore) snapshotLocked() []deployQueueEntry {
	out := make([]deployQueueEntry, 0, len(s.active)+len(s.history))
	for _, entry := range s.active {
		out = append(out, *cloneDeployQueueEntry(entry))
	}
	for _, entry := range s.history {
		out = append(out, *cloneDeployQueueEntry(entry))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

func (s *deployQueueStore) flushLocked() {
	if s.persist == nil {
		return
	}
	snapshot := make([]*deployQueueEntry, 0, len(s.active)+len(s.history))
	for _, entry := range s.active {
		snapshot = append(snapshot, cloneDeployQueueEntry(entry))
	}
	for _, entry := range s.history {
		snapshot = append(snapshot, cloneDeployQueueEntry(entry))
	}
	sort.SliceStable(snapshot, func(i, j int) bool {
		return snapshot[i].StartedAt.After(snapshot[j].StartedAt)
	})
	_ = s.persist(snapshot)
}

func (s *deployQueueStore) notifyLocked(snapshot deployQueueEntry) {
	if s.notify == nil {
		return
	}
	go s.notify(snapshot)
}

func cloneDeployQueueEntry(e *deployQueueEntry) *deployQueueEntry {
	if e == nil {
		return nil
	}
	out := *e
	if e.Containers != nil {
		out.Containers = append([]deployQueueContainerStatus(nil), e.Containers...)
	}
	if e.EndedAt != nil {
		end := *e.EndedAt
		out.EndedAt = &end
	}
	return &out
}

func generateDeployQueueID(e deployQueueEntry) string {
	return fmt.Sprintf("%s/%s/%s@%d", sanitizeDeployQueueIDPart(e.Tenant), sanitizeDeployQueueIDPart(e.Environment), sanitizeDeployQueueIDPart(e.Version), e.StartedAt.UnixNano())
}

func sanitizeDeployQueueIDPart(s string) string {
	if s == "" {
		return "_"
	}
	return s
}

func deployQueueStatePath(stateDir string) string {
	return filepath.Join(stateDir, "deploy_queue.json")
}

// defaultDeployQueueStatePath returns the platform user-config path the
// desktop persists deploy-queue state to. Returns "" when the OS doesn't
// expose a config dir; callers treat that as "skip persistence".
func defaultDeployQueueStatePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", "deploy_queue.json")
}

// loadDeployQueueStateFromDisk reads the persisted snapshot at path. Missing
// file is not an error — the desktop returns an empty list and starts fresh.
// Malformed JSON is logged via the caller's logger and treated as empty so a
// corrupt file can never block the desktop from starting.
func loadDeployQueueStateFromDisk(path string) ([]*deployQueueEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var entries []*deployQueueEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// writeDeployQueueStateAtomic writes the snapshot under a temp file then
// renames so a crash mid-write cannot truncate the persisted state.
func writeDeployQueueStateAtomic(path string, entries []*deployQueueEntry) error {
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
