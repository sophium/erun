package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// activityQueueStatus is the lifecycle phase of a tracked deploy. The desktop
// surfaces these states distinctly so the user can tell waiting/running/done
// apart at a glance.
type activityQueueStatus string

const (
	activityQueueStatusWaiting   activityQueueStatus = "waiting"
	activityQueueStatusRunning   activityQueueStatus = "running"
	activityQueueStatusSucceeded activityQueueStatus = "succeeded"
	activityQueueStatusFailed    activityQueueStatus = "failed"
	activityQueueStatusSkipped   activityQueueStatus = "skipped"
	activityQueueStatusCancelled activityQueueStatus = "cancelled"
)

// activityQueueStatusIsTerminal reports whether the status represents a
// finished entry that should not transition further.
func activityQueueStatusIsTerminal(s activityQueueStatus) bool {
	switch s {
	case activityQueueStatusSucceeded,
		activityQueueStatusFailed,
		activityQueueStatusSkipped,
		activityQueueStatusCancelled:
		return true
	}
	return false
}

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

// activityQueueEntry is a single tracked long-running command. Entries are
// rebuilt on every desktop launch from real cluster/host objects (helm
// releases, live PTY sessions); they are not persisted to disk.
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
	// Source identifies the real-world object that produced this entry,
	// such as "helm" for helm-release-derived deploys, "shell" for live
	// PTY sessions, "trace" for entries created from `==> Deploying`
	// PTY trace lines, or "action" for entries enqueued through the
	// desktop action runner. Used by recovery actions and the gating
	// logic to decide which lifecycle rules apply.
	Source string `json:"source,omitempty"`
	// SessionID is set for shell-source entries so the user can target
	// the live PTY session for kill/recovery.
	SessionID string `json:"sessionId,omitempty"`
	// ActionKind labels the user-action that produced this entry:
	// "open", "ai", "local", "init", "deploy", "force-deploy",
	// "sshd-init", "doctor", "reconnect-mcp", "open-ide",
	// "delete-environment", "cloud-context-start", "cloud-context-stop",
	// "cloud-context-init", "cloud-provider-init",
	// "cloud-provider-login", "cloud-provider-logout",
	// "cloud-provider-oidc-setup", "cloud-provider-alias-save".
	// Empty for observational entries (helm/trace/shell).
	ActionKind string `json:"actionKind,omitempty"`
	// EnqueuedAt is when the entry first entered the runner's waiting
	// queue. Set only on action-source entries.
	EnqueuedAt *time.Time `json:"enqueuedAt,omitempty"`
	// StartedRunningAt is when the entry was promoted from waiting to
	// running. Used by the drawer to show how long the user has been
	// blocked.
	StartedRunningAt *time.Time `json:"startedRunningAt,omitempty"`
}

// activityQueueStore keeps active and recent deploy entries. Callers
// mutate state through start/update/finish/dismiss; reads return cloned
// snapshots so callers can pass them to Wails event emitters without
// races. There is no persistence — state is reconstructed each desktop
// launch from real cluster/host objects (helm releases, live PTY
// sessions); see activity_helm_poller.go and activity_stale_sessions.go.
//
// Notifications: state-change events are forwarded through notifyCh to
// a single drain goroutine that calls notify(...) in arrival order.
// The previous design used `go notify(...)` per event, which let two
// independently-scheduled goroutines deliver `waiting` and `running`
// out of order — causing the frontend to settle on the older state.
// Serializing through one goroutine guarantees the frontend sees
// transitions in the order the store applied them.
type activityQueueStore struct {
	mu              sync.Mutex
	active          map[string]*activityQueueEntry
	history         []*activityQueueEntry
	now             func() time.Time
	notify          func(activityQueueEntry)
	notifyCh        chan activityQueueEntry
	closeNotifyOnce sync.Once
}

// activityQueueHistoryCapacity caps in-memory history length so the
// drawer doesn't grow unbounded over a long desktop session.
const activityQueueHistoryCapacity = 50

// activityQueueNotifyBuffer sizes the in-order notify pipeline. Sized
// well above expected burst depth (a few entries × per-card poll cadence)
// so the producer side never has to drop snapshots in normal use.
const activityQueueNotifyBuffer = 256

func newActivityQueueStore(notify func(activityQueueEntry), now func() time.Time) *activityQueueStore {
	if now == nil {
		now = time.Now
	}
	s := &activityQueueStore{
		active:   make(map[string]*activityQueueEntry),
		now:      now,
		notify:   notify,
		notifyCh: make(chan activityQueueEntry, activityQueueNotifyBuffer),
	}
	go s.runNotifyLoop()
	return s
}

// runNotifyLoop drains notifyCh in order, calling notify per snapshot.
// Exits when notifyCh is closed (closeNotifyLoop, called from App
// shutdown). A nil notify drains silently so unit tests that don't
// wire a notifier don't accumulate snapshots in the buffer.
func (s *activityQueueStore) runNotifyLoop() {
	for snapshot := range s.notifyCh {
		if s.notify != nil {
			s.notify(snapshot)
		}
	}
}

// closeNotifyLoop terminates the drain goroutine. Idempotent.
func (s *activityQueueStore) closeNotifyLoop() {
	s.closeNotifyOnce.Do(func() {
		close(s.notifyCh)
	})
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

// findRunning returns the first running (status=running) entry matching
// the predicate. Used for terminal-lock decisions, which only fire on
// running deploys.
func (s *activityQueueStore) findRunning(tenant, environment string) (activityQueueEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.active {
		if entry.Status != activityQueueStatusRunning {
			continue
		}
		if entry.Tenant == tenant && entry.Environment == environment {
			return *cloneActivityQueueEntry(entry), true
		}
	}
	return activityQueueEntry{}, false
}

// promoteToRunning moves a waiting entry into the running state and
// records StartedRunningAt. Returns the snapshot and true when the
// entry existed and was waiting; false when missing or already running
// (the latter is harmless — the runner's per-env worker pops one at a
// time, so promoting twice cannot happen normally).
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

// start registers a new active activity. If an entry with the same ID
// already exists, the existing entry is returned so the helm/shell
// reconciliation pollers and explicit registration paths can collapse
// into the same record without duplicates.
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
	s.notifyLocked(snapshot)
	return snapshot, true
}

// dismiss removes a finished entry from history. Active entries are never
// dismissed through this path — see forceDismiss for the user-driven
// override that handles stuck active entries.
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

// forceDismiss removes an entry from active or history regardless of
// status. The returned entry lets the caller decide on follow-up
// actions (e.g. killing a live PTY shell or running a helm
// clear-pending recovery). The removedFromActive boolean is true when
// the entry was active at the time of the call.
func (s *activityQueueStore) forceDismiss(id string) (entry activityQueueEntry, removedFromActive bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, found := s.active[id]; found {
		entry = *cloneActivityQueueEntry(existing)
		delete(s.active, id)
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

// notifyLocked enqueues the snapshot for in-order delivery. Caller must
// hold s.mu (the channel send is non-blocking, so the lock is held only
// briefly). On a full buffer the snapshot is dropped — frontend
// resilience is provided by the runner's belt-and-braces re-emit and
// by the pollers' periodic re-snapshots, so a single dropped event is
// recoverable.
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
