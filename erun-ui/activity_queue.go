package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// activityQueueStatus is the lifecycle phase of a tracked deploy.
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
// deploy's release.
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
	// Detail holds the captured command output behind a failed entry, so the
	// user can see why a deploy failed and copy a complete failure report.
	// Populated only when the entry finishes as failed; empty otherwise.
	Detail string `json:"detail,omitempty"`
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

// activityQueueStore keeps active and recent deploy entries. Reads return
// cloned snapshots so callers can hand them to Wails event emitters without
// races. There is no persistence — state is reconstructed each desktop launch
// from real cluster/host objects (helm releases, live PTY sessions).
//
// Notifications are serialized through a single drain goroutine so the frontend
// sees transitions in the order the store applied them. The previous per-event
// `go notify(...)` let independently-scheduled goroutines deliver `waiting` and
// `running` out of order, so the frontend settled on the older state.
type activityQueueStore struct {
	mu       sync.Mutex
	active   map[string]*activityQueueEntry
	history  []*activityQueueEntry
	now      func() time.Time
	notify   func(activityQueueEntry)
	notifyCh chan activityQueueEntry
	// outputByID buffers the most recent command output lines per active
	// entry ID. recordOutputLine appends while the entry runs; finish()
	// snapshots the tail into entry.Detail when the entry fails and drops the
	// buffer. Not persisted and not part of the frontend snapshot — only the
	// derived Detail crosses the wire.
	outputByID map[string][]string
}

// activityQueueHistoryCapacity caps in-memory history length so the
// drawer doesn't grow unbounded over a long desktop session.
const activityQueueHistoryCapacity = 50

// activityQueueNotifyBuffer sizes the in-order notify pipeline. Sized
// well above expected burst depth (a few entries × per-card poll cadence)
// so the producer side never has to drop snapshots in normal use.
const activityQueueNotifyBuffer = 256

// activityQueueOutputBufferLines caps how many of the most recent command
// output lines are retained per active entry for failure detail. Older lines
// are dropped from the front so a chatty or runaway command cannot grow the
// buffer without bound; the tail is what holds the actual error.
const activityQueueOutputBufferLines = 200

// activityQueueOutputLineMaxChars clips an individual captured line so a
// single pathological line (a megabyte of base64, say) cannot blow up the
// buffer. The failure context lives in the line's shape, not its full length.
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

// runNotifyLoop drains notifyCh for the store's lifetime. A nil notify still
// drains so unit tests without a notifier don't accumulate snapshots in the
// buffer.
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

// findActiveByCommand keys on command too, so the deploy-button gate can detect
// a same-version deploy independently of an unrelated build for the same env.
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

// latestDeployFailed reports whether the env's most recent deploy ended in
// failure — its runtime release is broken until a later deploy succeeds.
// Reconnect uses it to stop hammering a broken env with `erun open` retries
// whose pod will never come ready, independent of the `==> Deploy failed`
// ready-error that reconnectBlockedByDeployFailure keys on.
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

// promoteToRunning moves a waiting entry into the running state. Returning false
// for an already-running entry is harmless: the runner's per-env worker pops one
// at a time, so a double promotion cannot happen normally.
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

// recordOutputLine buffers command output for every active entry matching
// (tenant, environment), feeding entry.Detail if that entry later fails. A
// no-op when nothing matches: output before any entry registers (e.g. before
// "==> Deploying") or after all of them finish is not failure context.
//
// A caller cannot always name which command a given line belongs to (the
// build/push/deploy trace lines a subprocess or PTY interleaves are matched
// after the fact, not before), so this buffers into every active entry for
// the tenant/env rather than picking one arbitrarily. Normally only one entry
// is active at a time, so this is a no-op difference; when a build and a
// deploy are briefly active together for the same env, both get the shared
// context rather than the ambiguous single pick silently attaching it to the
// wrong one (the same map-iteration-order class of bug findActive had).
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
		buf := append(s.outputByID[entry.ID], line)
		if len(buf) > activityQueueOutputBufferLines {
			buf = buf[len(buf)-activityQueueOutputBufferLines:]
		}
		s.outputByID[entry.ID] = buf
	}
}

// finish moves an active entry into history. It is idempotent — returning false
// for an already-finished entry — to absorb duplicate "==> Deploy failed" /
// "==> Deployed" lines from the PTY tail.
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

// dismiss removes a finished entry from history only; stuck active entries go
// through forceDismiss, the user-driven override.
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

// forceDismiss removes an entry from active or history regardless of status,
// returning it so the caller can follow up — e.g. kill a live PTY shell or run
// a helm clear-pending recovery.
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

// notifyLocked enqueues the snapshot for in-order delivery; the caller must hold
// s.mu. On a full buffer the snapshot is dropped, which is safe because the
// runner's re-emit and the pollers' periodic re-snapshots recover a single
// dropped event.
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
