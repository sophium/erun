package eruncommon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

// An activity lease is how a long job says "I am the reason this environment is
// busy". Per-request activity cannot answer that: a detached build or agent run
// bumps nothing between its first and last second, so an environment under
// continuous heavy use reads as untouched. The lease is held for the job's
// lifetime and names the work, so the idle decision and the desktop both learn
// what is happening rather than only that a request arrived.
//
// Two bounds keep a lease from pinning an environment awake forever. It expires
// unless renewed, and — reusing the liveness shape session reconciliation
// already uses, an artifact plus a probe of the process behind it — a lease
// whose recorded holder is gone is reclaimed on the next read rather than
// waiting out its TTL.
//
// A plain lease is presence, not exclusion: many can be held at once, and
// nothing about taking one stops a second holder from taking another. An
// exclusive lease is the opposite claim — "nobody else is doing mutating work
// in this scope right now" — and exclusivity is scoped (`Scope`, "worktree" by
// default), never environment-wide, so two agents in two separate clones inside
// the same pod can each hold their own exclusive claim without colliding. Only
// a second claimant *in the same scope* is refused, and the refusal names the
// holder (see EnvironmentActivityLeaseConflictError) rather than merely failing.

const (
	// DefaultEnvironmentActivityLeaseTTL is long enough that a job renewing on
	// the monitor's cadence never lapses mid-run, short enough that an
	// uninstrumented crash releases the environment within one idle window.
	DefaultEnvironmentActivityLeaseTTL = 15 * time.Minute

	// DefaultExclusiveEnvironmentActivityLeaseTTL is shorter than the plain
	// default. PID liveness only reclaims a local holder; a remote one (an
	// orchestrator driving over MCP from another host) has no process here to
	// probe, so the TTL is the only reclaim path. Matching it to the
	// orchestrate skill's own five-minute polling cadence means a holder that
	// stops polling lapses promptly instead of locking a worktree for a
	// quarter of an hour after it stops renewing.
	DefaultExclusiveEnvironmentActivityLeaseTTL = 5 * time.Minute

	// defaultEnvironmentActivityLeaseScope is the scope an exclusive lease
	// claims when the caller does not name one more specifically — the whole
	// environment's one worktree, which is the common case.
	defaultEnvironmentActivityLeaseScope = "worktree"

	// EnvironmentActivityLeaseScopeEnvironment is the one scope that means "no
	// other work here at all", rather than "not this resource". It exists
	// because the contention that actually hurts is not a shared worktree: it
	// is the pod's own CPU and memory, which no worktree boundary divides. Two
	// gate batches in separate clones of the same repo still fight over the
	// same cores, and a gate that takes 7 minutes alone took 17 and went red on
	// two unrelated tests when a second one ran beside it. Work that declares
	// this scope is refused a second holder, and — uniquely for this scope —
	// so is any job started while it is held (see job_exclusive.go).
	EnvironmentActivityLeaseScopeEnvironment = "environment"

	// EnvironmentActivityLeaseMaxLifetime is the hard ceiling a renewal cannot
	// push past. Without it a holder that renews forever — or a wrapper looping
	// on a hung job — would keep an environment awake indefinitely, which is the
	// failure this whole mechanism exists to make impossible.
	EnvironmentActivityLeaseMaxLifetime = 12 * time.Hour

	// environmentActivityLeaseMarker is the idle marker leases report through, so
	// a held lease blocks idle-stop by the same predicate every other signal uses.
	environmentActivityLeaseMarker = "lease"

	environmentActivityLeaseDirName          = "leases"
	environmentActivityExclusiveLeaseDirName = "exclusive"
)

// EnvironmentActivityLeaseHolder identifies who is claiming a lease, so a
// refusal can name someone to go ask rather than merely failing. Tenant and
// User come from the caller's resolved auth identity, never from caller input
// directly, so a lease cannot be taken out in someone else's name.
type EnvironmentActivityLeaseHolder struct {
	Orchestrator string `json:"orchestrator,omitempty"`
	Tenant       string `json:"tenant,omitempty"`
	User         string `json:"user,omitempty"`
}

// String renders the holder for an error message or a busy detail line.
func (h EnvironmentActivityLeaseHolder) String() string {
	parts := make([]string, 0, 3)
	if v := strings.TrimSpace(h.Orchestrator); v != "" {
		parts = append(parts, "orchestrator "+v)
	}
	if v := strings.TrimSpace(h.User); v != "" {
		parts = append(parts, "user "+v)
	}
	if v := strings.TrimSpace(h.Tenant); v != "" {
		parts = append(parts, "tenant "+v)
	}
	if len(parts) == 0 {
		return "an unnamed holder"
	}
	return strings.Join(parts, ", ")
}

// EnvironmentActivityLease is one held claim on an environment.
type EnvironmentActivityLease struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// PID is the holder process, 0 when the lease is held by something with no
	// process to watch (an agent that took it explicitly). A recorded PID that no
	// longer exists reclaims the lease immediately.
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	RenewedAt time.Time `json:"renewedAt,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
	// Scope and Exclusive are set only on an exclusive claim. Scope names the
	// resource being protected ("worktree" by default); Exclusive is true so a
	// reader can tell an exclusive claim apart from ordinary presence without
	// inspecting Scope.
	Scope     string                         `json:"scope,omitempty"`
	Exclusive bool                           `json:"exclusive,omitempty"`
	Holder    EnvironmentActivityLeaseHolder `json:"holder,omitempty"`
}

// TakeEnvironmentActivityLeaseParams is the input to taking or renewing a lease.
// Taking an existing ID renews it, so a wrapper can refresh on a timer without
// tracking whether it already holds one.
type TakeEnvironmentActivityLeaseParams struct {
	Tenant      string
	Environment string
	Name        string
	ID          string
	PID         int
	TTL         time.Duration
	Now         time.Time
	// Scope and Exclusive request the exclusive-claim mode. Scope defaults to
	// "worktree" when Exclusive is set and no scope is named.
	Scope     string
	Exclusive bool
	Holder    EnvironmentActivityLeaseHolder
}

// normalize fills the defaults and rejects the inputs that would produce a
// lease nobody can act on: one that names no work, or one that never lapses.
func (p TakeEnvironmentActivityLeaseParams) normalize() (TakeEnvironmentActivityLeaseParams, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return p, fmt.Errorf("lease name is required")
	}
	id, err := ResolveEnvironmentActivityLeaseID(p.ID, p.Name)
	if err != nil {
		return p, err
	}
	p.ID = id
	if p.Exclusive {
		p.Scope = NormalizeExclusiveEnvironmentActivityLeaseScope(p.Scope)
	} else {
		p.Scope = strings.TrimSpace(p.Scope)
	}
	if p.TTL == 0 {
		if p.Exclusive {
			p.TTL = DefaultExclusiveEnvironmentActivityLeaseTTL
		} else {
			p.TTL = DefaultEnvironmentActivityLeaseTTL
		}
	}
	if p.TTL < 0 {
		return p, fmt.Errorf("lease ttl must be greater than zero")
	}
	if p.PID < 0 {
		return p, fmt.Errorf("lease pid must not be negative")
	}
	if p.Now.IsZero() {
		p.Now = time.Now()
	}
	return p, nil
}

// TakeEnvironmentActivityLease writes the lease and returns what was persisted.
func TakeEnvironmentActivityLease(params TakeEnvironmentActivityLeaseParams) (EnvironmentActivityLease, error) {
	params, err := params.normalize()
	if err != nil {
		return EnvironmentActivityLease{}, err
	}
	if params.Exclusive {
		return takeExclusiveEnvironmentActivityLease(params)
	}
	return takeSharedEnvironmentActivityLease(params)
}

// takeSharedEnvironmentActivityLease is plain presence: any number of holders
// coexist, keyed by id, create-or-overwrite. This is the pre-existing lease
// behavior, unchanged.
func takeSharedEnvironmentActivityLease(params TakeEnvironmentActivityLeaseParams) (EnvironmentActivityLease, error) {
	id, name, now := params.ID, params.Name, params.Now
	dir, err := environmentActivityLeaseDir(params.Tenant, params.Environment)
	if err != nil {
		return EnvironmentActivityLease{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return EnvironmentActivityLease{}, err
	}

	path := filepath.Join(dir, id+".json")
	lease := EnvironmentActivityLease{ID: id, Name: name, PID: params.PID, StartedAt: now, Holder: params.Holder}
	// Only a lease still held is renewed. Reusing an id whose previous holder
	// is gone starts a fresh claim, or the new lease would inherit a start far
	// enough in the past to be dead on arrival.
	if existing, err := loadEnvironmentActivityLease(path); err == nil && environmentActivityLeaseHeld(existing, now, processAlive) {
		lease.StartedAt = existing.StartedAt
		lease.RenewedAt = now
	}
	lease.ExpiresAt = capEnvironmentActivityLeaseExpiry(lease.StartedAt, now.Add(params.TTL))

	return writeEnvironmentActivityLease(path, lease)
}

// EnvironmentActivityLeaseConflictError is returned when an exclusive take
// finds a different, still-held exclusive claim in the same scope. It names
// the holder so a caller can report who to go ask, rather than failing
// mysteriously.
type EnvironmentActivityLeaseConflictError struct {
	Scope  string
	Holder EnvironmentActivityLease
}

func (e *EnvironmentActivityLeaseConflictError) Error() string {
	return fmt.Sprintf("exclusive lease scope %q is already held by %s (%s, id %s)",
		e.Scope, e.Holder.Holder.String(), e.Holder.Name, e.Holder.ID)
}

// EnvironmentOperatorPresentError is returned when a fresh exclusive lease
// take is refused because an operator is already present in the environment.
// The operator never takes a lease, so this refusal is inferred from activity
// markers (see EnvironmentOperatorPresenceReason) rather than from a
// competing claim, and only applies to a fresh claim — never to a caller
// renewing a claim it already holds.
type EnvironmentOperatorPresentError struct {
	Reason string
}

func (e *EnvironmentOperatorPresentError) Error() string {
	return fmt.Sprintf("refusing exclusive lease: %s", e.Reason)
}

// takeExclusiveEnvironmentActivityLease enforces "at most one exclusive holder
// per scope". The file is keyed by scope, not by id, so a second holder's
// take necessarily lands on the same path as the first and one of two things
// happens: it is the same holder renewing (same id), or it is a genuine
// conflict. Atomicity comes from os.O_EXCL: the file is created only when it
// does not already exist, so of any number of concurrent claimants racing to
// create it, exactly one create succeeds.
func takeExclusiveEnvironmentActivityLease(params TakeEnvironmentActivityLeaseParams) (EnvironmentActivityLease, error) {
	id, now, scope := params.ID, params.Now, params.Scope
	path, err := prepareExclusiveEnvironmentActivityLeasePath(params.Tenant, params.Environment, scope)
	if err != nil {
		return EnvironmentActivityLease{}, err
	}

	lease := EnvironmentActivityLease{
		ID: id, Name: params.Name, PID: params.PID, StartedAt: now,
		Scope: scope, Exclusive: true, Holder: params.Holder,
	}

	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		decision, existing, staleRecordPresent := decideExclusiveEnvironmentActivityLeaseClaim(path, id, now)
		if decision == exclusiveClaimConflict {
			return EnvironmentActivityLease{}, &EnvironmentActivityLeaseConflictError{Scope: scope, Holder: existing}
		}
		if decision == exclusiveClaimRenew {
			return renewExclusiveEnvironmentActivityLeaseClaim(path, lease, existing, params.TTL, now)
		}
		// exclusiveClaimFree: nobody holds the scope, or the holder is stale.
		claimed, updated, err := tryClaimFreeExclusiveEnvironmentActivityLeaseScope(path, lease, params.TTL, now, staleRecordPresent)
		if err != nil {
			return EnvironmentActivityLease{}, err
		}
		if claimed {
			return updated, nil
		}
		// Somebody else's create landed first between our check and our own
		// attempt; loop around and read who holds it now.
	}
	return EnvironmentActivityLease{}, exclusiveEnvironmentActivityLeaseContendedError(path, scope)
}

func prepareExclusiveEnvironmentActivityLeasePath(tenant, environment, scope string) (string, error) {
	dir, err := exclusiveEnvironmentActivityLeaseDir(tenant, environment)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return exclusiveEnvironmentActivityLeasePath(tenant, environment, scope)
}

func renewExclusiveEnvironmentActivityLeaseClaim(path string, lease, existing EnvironmentActivityLease, ttl time.Duration, now time.Time) (EnvironmentActivityLease, error) {
	lease.StartedAt = existing.StartedAt
	lease.RenewedAt = now
	lease.ExpiresAt = capEnvironmentActivityLeaseExpiry(lease.StartedAt, now.Add(ttl))
	return writeEnvironmentActivityLease(path, lease)
}

// tryClaimFreeExclusiveEnvironmentActivityLeaseScope reclaims a stale file —
// only when the caller's own read actually found one — and then races every
// other concurrent claimant through the one operation that is actually
// atomic: creating a file that must not already exist. claimed is false with
// a nil error when another claimant's create won the race instead.
func tryClaimFreeExclusiveEnvironmentActivityLeaseScope(path string, lease EnvironmentActivityLease, ttl time.Duration, now time.Time, staleRecordPresent bool) (bool, EnvironmentActivityLease, error) {
	if staleRecordPresent {
		_ = os.Remove(path)
	}
	lease.ExpiresAt = capEnvironmentActivityLeaseExpiry(lease.StartedAt, now.Add(ttl))
	created, err := createExclusiveEnvironmentActivityLeaseFile(path, lease)
	if err != nil {
		return false, EnvironmentActivityLease{}, err
	}
	return created, lease, nil
}

// exclusiveEnvironmentActivityLeaseContendedError reports whoever holds the
// scope once the retry budget is exhausted, so a caller unlucky enough to
// keep losing the create race still gets a refusal that names a holder
// rather than a bare "contended".
func exclusiveEnvironmentActivityLeaseContendedError(path, scope string) error {
	existing, err := loadEnvironmentActivityLease(path)
	if err == nil {
		return &EnvironmentActivityLeaseConflictError{Scope: scope, Holder: existing}
	}
	return fmt.Errorf("could not take exclusive lease for scope %q: contended", scope)
}

// exclusiveEnvironmentActivityLeaseClaim is what an exclusive take must do
// given the scope's current on-disk state.
type exclusiveEnvironmentActivityLeaseClaim int

const (
	exclusiveClaimFree     exclusiveEnvironmentActivityLeaseClaim = iota // nobody holds it, or the holder is stale
	exclusiveClaimRenew                                                  // this same id already holds it
	exclusiveClaimConflict                                               // a different, still-held id holds it
)

// decideExclusiveEnvironmentActivityLeaseClaim reads the scope's current
// file at most once and reports both the decision and whether that read
// actually found a (necessarily stale, in the Free case) record — the
// caller's cue for whether removing the file before its own create attempt
// is reclaiming something or racing against nothing. Skipping the remove
// when nothing was read matters: a concurrent creator's file landing between
// this read and the caller's create must lose to O_CREATE|O_EXCL's own EEXIST,
// never be clobbered by a remove this call had no evidence to justify.
func decideExclusiveEnvironmentActivityLeaseClaim(path, id string, now time.Time) (exclusiveEnvironmentActivityLeaseClaim, EnvironmentActivityLease, bool) {
	existing, err := loadEnvironmentActivityLease(path)
	staleRecordPresent := err == nil
	if !staleRecordPresent || !environmentActivityLeaseHeld(existing, now, processAlive) {
		return exclusiveClaimFree, EnvironmentActivityLease{}, staleRecordPresent
	}
	if existing.ID == id {
		return exclusiveClaimRenew, existing, true
	}
	return exclusiveClaimConflict, existing, true
}

// createExclusiveEnvironmentActivityLeaseFile is the atomic primitive the
// exclusive mode depends on: created is false with a nil error when another
// claimant's create won the race, distinct from a real I/O failure.
func createExclusiveEnvironmentActivityLeaseFile(path string, lease EnvironmentActivityLease) (bool, error) {
	data, err := json.MarshalIndent(lease, "", "  ")
	if err != nil {
		return false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, err
	}
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return false, writeErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	return true, nil
}

func writeEnvironmentActivityLease(path string, lease EnvironmentActivityLease) (EnvironmentActivityLease, error) {
	data, err := json.MarshalIndent(lease, "", "  ")
	if err != nil {
		return EnvironmentActivityLease{}, err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return EnvironmentActivityLease{}, err
	}
	return lease, nil
}

// ReleaseEnvironmentActivityLease drops a shared (non-exclusive) lease.
// Idempotent: releasing a lease that already expired or was never taken is
// success, so a wrapper's exit trap never fails a job that already finished
// cleanly.
func ReleaseEnvironmentActivityLease(tenant, environment, id string) error {
	resolved, err := ResolveEnvironmentActivityLeaseID(id, id)
	if err != nil {
		return err
	}
	dir, err := environmentActivityLeaseDir(tenant, environment)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, resolved+".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ReleaseExclusiveEnvironmentActivityLease drops an exclusive claim on a
// scope. It only removes the file when the recorded holder is this same id —
// releasing by scope name alone, without proving you are the holder, could
// otherwise drop a different holder's exclusivity out from under them (a
// stale release call racing a new legitimate claim). A mismatched or already
// vacated scope is success, matching the shared release's idempotence.
func ReleaseExclusiveEnvironmentActivityLease(tenant, environment, scope, id string) error {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = defaultEnvironmentActivityLeaseScope
	}
	resolvedID, err := ResolveEnvironmentActivityLeaseID(id, id)
	if err != nil {
		return err
	}
	path, err := exclusiveEnvironmentActivityLeasePath(tenant, environment, scope)
	if err != nil {
		return err
	}
	existing, err := loadEnvironmentActivityLease(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if existing.ID != resolvedID {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// LoadEnvironmentActivityLeases returns the leases still holding the
// environment, reclaiming expired and orphaned ones as it reads.
func LoadEnvironmentActivityLeases(tenant, environment string, now time.Time) ([]EnvironmentActivityLease, error) {
	return loadEnvironmentActivityLeases(tenant, environment, now, processAlive)
}

func loadEnvironmentActivityLeases(tenant, environment string, now time.Time, alive func(int) bool) ([]EnvironmentActivityLease, error) {
	dir, err := environmentActivityLeaseDir(tenant, environment)
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	held, err := collectHeldEnvironmentActivityLeases(dir, now, alive)
	if err != nil {
		return nil, err
	}
	// Exclusive claims live in their own subdirectory (keyed by scope, not
	// id), so they are collected separately and folded into the same held
	// set - a caller reading "what is holding this environment" should see
	// both kinds without knowing the on-disk split exists.
	exclusiveDir, err := exclusiveEnvironmentActivityLeaseDir(tenant, environment)
	if err != nil {
		return nil, err
	}
	exclusiveHeld, err := collectHeldEnvironmentActivityLeases(exclusiveDir, now, alive)
	if err != nil {
		return nil, err
	}
	held = append(held, exclusiveHeld...)
	sort.Slice(held, func(i, j int) bool {
		if held[i].Name != held[j].Name {
			return held[i].Name < held[j].Name
		}
		return held[i].ID < held[j].ID
	})
	return held, nil
}

// collectHeldEnvironmentActivityLeases reads every still-held lease file in
// dir, reclaiming expired and orphaned ones as it goes. A missing directory
// is "no leases", not an error - the subdirectory for exclusive claims may
// never have been created.
func collectHeldEnvironmentActivityLeases(dir string, now time.Time, alive func(int) bool) ([]EnvironmentActivityLease, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var held []EnvironmentActivityLease
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if lease, ok := readHeldEnvironmentActivityLease(filepath.Join(dir, entry.Name()), now, alive); ok {
			held = append(held, lease)
		}
	}
	return held, nil
}

// readHeldEnvironmentActivityLease returns the lease when it is still held, and
// reclaims the file otherwise. A lease we cannot read cannot be shown to be
// held, and leaving it would block idle-stop on an unreadable claim forever.
func readHeldEnvironmentActivityLease(path string, now time.Time, alive func(int) bool) (EnvironmentActivityLease, bool) {
	lease, err := loadEnvironmentActivityLease(path)
	if err != nil || !environmentActivityLeaseHeld(lease, now, alive) {
		_ = os.Remove(path)
		return EnvironmentActivityLease{}, false
	}
	return lease, true
}

// environmentActivityLeaseHeld is the whole liveness rule: not past its expiry,
// not past the hard lifetime ceiling, and — when it named a holder — that holder
// still exists.
func environmentActivityLeaseHeld(lease EnvironmentActivityLease, now time.Time, alive func(int) bool) bool {
	if strings.TrimSpace(lease.ID) == "" {
		return false
	}
	if !lease.ExpiresAt.IsZero() && !now.Before(lease.ExpiresAt) {
		return false
	}
	if !lease.StartedAt.IsZero() && now.Sub(lease.StartedAt) >= EnvironmentActivityLeaseMaxLifetime {
		return false
	}
	if lease.PID > 0 && alive != nil && !alive(lease.PID) {
		return false
	}
	return true
}

func capEnvironmentActivityLeaseExpiry(startedAt, expiresAt time.Time) time.Time {
	if startedAt.IsZero() {
		return expiresAt
	}
	ceiling := startedAt.Add(EnvironmentActivityLeaseMaxLifetime)
	if expiresAt.After(ceiling) {
		return ceiling
	}
	return expiresAt
}

// ResolveEnvironmentActivityLeaseID resolves a caller-supplied lease id (or
// its fallback, typically the lease name) to the sanitized form the store
// persists and keys files by. A caller that needs to compare a raw id against
// a stored lease's ID must resolve it through here first — comparing the raw
// value against the sanitized one is how erun#1652 happened.
func ResolveEnvironmentActivityLeaseID(id, fallback string) (string, error) {
	value := strings.TrimSpace(id)
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	if value == "" {
		return "", fmt.Errorf("lease id is required")
	}
	sanitized := sanitizeForFilename(value)
	if sanitized == "" || sanitized == "_" || sanitized == "." || sanitized == ".." {
		return "", fmt.Errorf("lease id %q has no usable characters", value)
	}
	return sanitized, nil
}

// NormalizeExclusiveEnvironmentActivityLeaseScope trims scope and defaults it
// to "worktree" when empty, exactly as an exclusive take's own params
// normalisation does before persisting — so a caller comparing against a
// stored lease's Scope agrees with what will actually be written.
func NormalizeExclusiveEnvironmentActivityLeaseScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = defaultEnvironmentActivityLeaseScope
	}
	return scope
}

func environmentActivityLeaseDir(tenant, environment string) (string, error) {
	dir, err := EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, environmentActivityLeaseDirName), nil
}

// exclusiveEnvironmentActivityLeaseDir is a subdirectory of the ordinary
// leases dir, kept separate because its files are keyed by scope rather than
// by holder id.
func exclusiveEnvironmentActivityLeaseDir(tenant, environment string) (string, error) {
	dir, err := environmentActivityLeaseDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, environmentActivityExclusiveLeaseDirName), nil
}

func exclusiveEnvironmentActivityLeasePath(tenant, environment, scope string) (string, error) {
	dir, err := exclusiveEnvironmentActivityLeaseDir(tenant, environment)
	if err != nil {
		return "", err
	}
	sanitized := sanitizeForFilename(strings.TrimSpace(scope))
	if sanitized == "" || sanitized == "_" || sanitized == "." || sanitized == ".." {
		sanitized = defaultEnvironmentActivityLeaseScope
	}
	return filepath.Join(dir, sanitized+".json"), nil
}

func loadEnvironmentActivityLease(path string) (EnvironmentActivityLease, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return EnvironmentActivityLease{}, err
	}
	var lease EnvironmentActivityLease
	if err := json.Unmarshal(data, &lease); err != nil {
		return EnvironmentActivityLease{}, err
	}
	return lease, nil
}

// leaseIdleMarker folds the held leases into the same marker shape every other
// activity signal reports through, so a held lease blocks idle-stop without the
// stop predicate needing to know leases exist.
func leaseIdleMarker(leases []EnvironmentActivityLease, now time.Time) EnvironmentIdleMarker {
	marker := EnvironmentIdleMarker{Name: environmentActivityLeaseMarker}
	if len(leases) == 0 {
		marker.Idle = true
		marker.Reason = "no work leases held"
		return marker
	}
	names := make([]string, 0, len(leases))
	var lastRenewed time.Time
	var expiry time.Time
	for _, lease := range leases {
		names = append(names, lease.Name)
		if lease.RenewedAt.After(lastRenewed) {
			lastRenewed = lease.RenewedAt
		}
		if lease.StartedAt.After(lastRenewed) {
			lastRenewed = lease.StartedAt
		}
		if lease.ExpiresAt.After(expiry) {
			expiry = lease.ExpiresAt
		}
	}
	marker.Idle = false
	marker.Reason = "held by " + strings.Join(names, ", ")
	marker.LastActivity = lastRenewed
	marker.LastSeen = lastRenewed
	marker.SecondsRemaining = secondsRemaining(expiry.Sub(now))
	return marker
}

// processAlive reports whether a lease's recorded holder still exists. Signal 0
// is the portable "does this pid exist" probe on unix — EPERM means the process
// is there but owned by someone else, which still counts as alive. Windows has
// no signals, so os.FindProcess failing is the only answer available there.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true
	}
	return runtime.GOOS == "windows"
}
