package activitylease

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

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// An activity lease names the long job keeping an environment busy, which per-request
// activity cannot see. It expires unless renewed and is reclaimed early when its holder
// process is gone. A plain lease is presence; an exclusive lease refuses a second holder
// in the same scope only.

const (
	// DefaultEnvironmentActivityLeaseTTL outlasts a renewal interval but frees a crashed job
	// within one idle window.
	DefaultEnvironmentActivityLeaseTTL = 15 * time.Minute

	// DefaultExclusiveEnvironmentActivityLeaseTTL is short because a remote holder has no PID
	// to probe; the TTL is its only reclaim path.
	DefaultExclusiveEnvironmentActivityLeaseTTL = 5 * time.Minute

	// DefaultEnvironmentActivityLeaseScope is the default exclusive scope.
	DefaultEnvironmentActivityLeaseScope = "worktree"

	// EnvironmentActivityLeaseScopeEnvironment claims the whole pod, since CPU and memory
	// contention crosses worktrees; it also blocks new jobs (see job_exclusive.go).
	EnvironmentActivityLeaseScopeEnvironment = "environment"

	// EnvironmentActivityLeaseMaxLifetime caps renewals so no holder keeps an environment awake forever.
	EnvironmentActivityLeaseMaxLifetime = 12 * time.Hour

	environmentActivityLeaseDirName          = "leases"
	environmentActivityExclusiveLeaseDirName = "exclusive"
)

// TakeEnvironmentActivityLeaseParams takes a lease; taking an existing ID renews it.
type TakeEnvironmentActivityLeaseParams struct {
	Tenant      string
	Environment string
	Name        string
	ID          string
	PID         int
	TTL         time.Duration
	Now         time.Time
	// Scope defaults to "worktree" when Exclusive is set.
	Scope     string
	Exclusive bool
	Holder    model.EnvironmentActivityLeaseHolder
}

// normalize rejects a lease that names no work or never lapses.
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
func TakeEnvironmentActivityLease(params TakeEnvironmentActivityLeaseParams) (model.EnvironmentActivityLease, error) {
	params, err := params.normalize()
	if err != nil {
		return model.EnvironmentActivityLease{}, err
	}
	if params.Exclusive {
		return takeExclusiveEnvironmentActivityLease(params)
	}
	return takeSharedEnvironmentActivityLease(params)
}

func takeSharedEnvironmentActivityLease(params TakeEnvironmentActivityLeaseParams) (model.EnvironmentActivityLease, error) {
	id, name, now := params.ID, params.Name, params.Now
	dir, err := environmentActivityLeaseDir(params.Tenant, params.Environment)
	if err != nil {
		return model.EnvironmentActivityLease{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return model.EnvironmentActivityLease{}, err
	}

	path := filepath.Join(dir, id+".json")
	lease := model.EnvironmentActivityLease{ID: id, Name: name, PID: params.PID, StartedAt: now, Holder: params.Holder}
	// A dead holder's id starts a fresh claim, or it would inherit an expired start.
	if existing, err := loadEnvironmentActivityLease(path); err == nil && environmentActivityLeaseHeld(existing, now, ProcessAlive) {
		lease.StartedAt = existing.StartedAt
		lease.RenewedAt = now
	}
	lease.ExpiresAt = capEnvironmentActivityLeaseExpiry(lease.StartedAt, now.Add(params.TTL))

	return writeEnvironmentActivityLease(path, lease)
}

// EnvironmentActivityLeaseConflictError names the holder of a conflicting exclusive claim.
type EnvironmentActivityLeaseConflictError struct {
	Scope  string
	Holder model.EnvironmentActivityLease
}

func (e *EnvironmentActivityLeaseConflictError) Error() string {
	return fmt.Sprintf("exclusive lease scope %q is already held by %s (%s, id %s)",
		e.Scope, e.Holder.Holder.String(), e.Holder.Name, e.Holder.ID)
}

// takeExclusiveEnvironmentActivityLease keys the file by scope so rivals collide on one
// path; the exclusive create decides the race.
func takeExclusiveEnvironmentActivityLease(params TakeEnvironmentActivityLeaseParams) (model.EnvironmentActivityLease, error) {
	id, now, scope := params.ID, params.Now, params.Scope
	path, err := prepareExclusiveEnvironmentActivityLeasePath(params.Tenant, params.Environment, scope)
	if err != nil {
		return model.EnvironmentActivityLease{}, err
	}

	lease := model.EnvironmentActivityLease{
		ID: id, Name: params.Name, PID: params.PID, StartedAt: now,
		Scope: scope, Exclusive: true, Holder: params.Holder,
	}

	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		decision, existing, staleRecordPresent := decideExclusiveEnvironmentActivityLeaseClaim(path, id, now)
		if decision == exclusiveClaimConflict {
			return model.EnvironmentActivityLease{}, &EnvironmentActivityLeaseConflictError{Scope: scope, Holder: existing}
		}
		if decision == exclusiveClaimRenew {
			return renewExclusiveEnvironmentActivityLeaseClaim(path, lease, existing, params.TTL, now)
		}
		claimed, updated, err := tryClaimFreeExclusiveEnvironmentActivityLeaseScope(path, lease, params.TTL, now, staleRecordPresent)
		if err != nil {
			return model.EnvironmentActivityLease{}, err
		}
		if claimed {
			return updated, nil
		}
		// Another create won between our check and attempt; re-read the holder.
	}
	return model.EnvironmentActivityLease{}, exclusiveEnvironmentActivityLeaseContendedError(path, scope)
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

func renewExclusiveEnvironmentActivityLeaseClaim(path string, lease, existing model.EnvironmentActivityLease, ttl time.Duration, now time.Time) (model.EnvironmentActivityLease, error) {
	lease.StartedAt = existing.StartedAt
	lease.RenewedAt = now
	lease.ExpiresAt = capEnvironmentActivityLeaseExpiry(lease.StartedAt, now.Add(ttl))
	return writeEnvironmentActivityLease(path, lease)
}

// tryClaimFreeExclusiveEnvironmentActivityLeaseScope removes a stale file only when one was read,
// then races the exclusive create; claimed=false with nil error means it lost.
func tryClaimFreeExclusiveEnvironmentActivityLeaseScope(path string, lease model.EnvironmentActivityLease, ttl time.Duration, now time.Time, staleRecordPresent bool) (bool, model.EnvironmentActivityLease, error) {
	if staleRecordPresent {
		_ = os.Remove(path)
	}
	lease.ExpiresAt = capEnvironmentActivityLeaseExpiry(lease.StartedAt, now.Add(ttl))
	created, err := createExclusiveEnvironmentActivityLeaseFile(path, lease)
	if err != nil {
		return false, model.EnvironmentActivityLease{}, err
	}
	return created, lease, nil
}

// exclusiveEnvironmentActivityLeaseContendedError names the holder once retries run out.
func exclusiveEnvironmentActivityLeaseContendedError(path, scope string) error {
	existing, err := loadEnvironmentActivityLease(path)
	if err == nil {
		return &EnvironmentActivityLeaseConflictError{Scope: scope, Holder: existing}
	}
	return fmt.Errorf("could not take exclusive lease for scope %q: contended", scope)
}

type exclusiveEnvironmentActivityLeaseClaim int

const (
	exclusiveClaimFree exclusiveEnvironmentActivityLeaseClaim = iota
	exclusiveClaimRenew
	exclusiveClaimConflict
)

// decideExclusiveEnvironmentActivityLeaseClaim also reports whether a stale record was read;
// without one the caller must not remove, or it could clobber a concurrent creator.
func decideExclusiveEnvironmentActivityLeaseClaim(path, id string, now time.Time) (exclusiveEnvironmentActivityLeaseClaim, model.EnvironmentActivityLease, bool) {
	existing, err := loadEnvironmentActivityLease(path)
	staleRecordPresent := err == nil
	if !staleRecordPresent || !environmentActivityLeaseHeld(existing, now, ProcessAlive) {
		return exclusiveClaimFree, model.EnvironmentActivityLease{}, staleRecordPresent
	}
	if existing.ID == id {
		return exclusiveClaimRenew, existing, true
	}
	return exclusiveClaimConflict, existing, true
}

// createExclusiveEnvironmentActivityLeaseFile publishes via hard link so no empty claim is
// ever visible (a reader reclaims unparseable leases). created=false means it lost the race.
func createExclusiveEnvironmentActivityLeaseFile(path string, lease model.EnvironmentActivityLease) (bool, error) {
	data, err := json.MarshalIndent(lease, "", "  ")
	if err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	_, writeErr := tmp.Write(append(data, '\n'))
	closeErr := tmp.Close()
	if writeErr != nil {
		return false, writeErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return false, err
	}
	if err := os.Link(tmpName, path); err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, err
	}
	exclusiveLeaseClaimVisible(path)
	return true, nil
}

// exclusiveLeaseClaimVisible is a test hook fired when a new claim first exists.
var exclusiveLeaseClaimVisible = func(string) {}

func writeEnvironmentActivityLease(path string, lease model.EnvironmentActivityLease) (model.EnvironmentActivityLease, error) {
	data, err := json.MarshalIndent(lease, "", "  ")
	if err != nil {
		return model.EnvironmentActivityLease{}, err
	}
	// Replace atomically: a reader reclaims an unparseable lease, so a torn renewal would delete it.
	if err := config.ReplaceFileAtomically(path, append(data, '\n'), 0o644); err != nil {
		return model.EnvironmentActivityLease{}, err
	}
	return lease, nil
}

// LoadEnvironmentActivityLeases returns held leases, reclaiming expired and orphaned ones.
func LoadEnvironmentActivityLeases(tenant, environment string, now time.Time) ([]model.EnvironmentActivityLease, error) {
	return loadEnvironmentActivityLeases(tenant, environment, now, ProcessAlive)
}

func loadEnvironmentActivityLeases(tenant, environment string, now time.Time, alive func(int) bool) ([]model.EnvironmentActivityLease, error) {
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
	// Exclusive claims live in a separate scope-keyed directory.
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

// collectHeldEnvironmentActivityLeases treats a missing directory as no leases.
func collectHeldEnvironmentActivityLeases(dir string, now time.Time, alive func(int) bool) ([]model.EnvironmentActivityLease, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var held []model.EnvironmentActivityLease
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

// readHeldEnvironmentActivityLease reclaims an unreadable lease, or it would block idle-stop forever.
func readHeldEnvironmentActivityLease(path string, now time.Time, alive func(int) bool) (model.EnvironmentActivityLease, bool) {
	lease, err := loadEnvironmentActivityLease(path)
	if err != nil || !environmentActivityLeaseHeld(lease, now, alive) {
		_ = os.Remove(path)
		return model.EnvironmentActivityLease{}, false
	}
	return lease, true
}

func environmentActivityLeaseHeld(lease model.EnvironmentActivityLease, now time.Time, alive func(int) bool) bool {
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

// ResolveEnvironmentActivityLeaseID sanitizes an id as the store keys it; raw ids never match stored ones.
func ResolveEnvironmentActivityLeaseID(id, fallback string) (string, error) {
	value := strings.TrimSpace(id)
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	if value == "" {
		return "", fmt.Errorf("lease id is required")
	}
	sanitized := core.SanitizeForFilename(value)
	if sanitized == "" || sanitized == "_" || sanitized == "." || sanitized == ".." {
		return "", fmt.Errorf("lease id %q has no usable characters", value)
	}
	return sanitized, nil
}

// NormalizeExclusiveEnvironmentActivityLeaseScope applies the same default an exclusive take persists.
func NormalizeExclusiveEnvironmentActivityLeaseScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = DefaultEnvironmentActivityLeaseScope
	}
	return scope
}

func environmentActivityLeaseDir(tenant, environment string) (string, error) {
	dir, err := activity.EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, environmentActivityLeaseDirName), nil
}

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
	sanitized := core.SanitizeForFilename(strings.TrimSpace(scope))
	if sanitized == "" || sanitized == "_" || sanitized == "." || sanitized == ".." {
		sanitized = DefaultEnvironmentActivityLeaseScope
	}
	return filepath.Join(dir, sanitized+".json"), nil
}

func loadEnvironmentActivityLease(path string) (model.EnvironmentActivityLease, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.EnvironmentActivityLease{}, err
	}
	var lease model.EnvironmentActivityLease
	if err := json.Unmarshal(data, &lease); err != nil {
		return model.EnvironmentActivityLease{}, err
	}
	return lease, nil
}

// FormatLeaseHolders renders one clause per distinct holder, listing its leases.
func FormatLeaseHolders(leases []model.EnvironmentActivityLease) string {
	type leaseHolderGroup struct {
		holder model.EnvironmentActivityLeaseHolder
		names  []string
	}
	var groups []leaseHolderGroup
	index := make(map[model.EnvironmentActivityLeaseHolder]int)
	for _, lease := range leases {
		i, ok := index[lease.Holder]
		if !ok {
			i = len(groups)
			index[lease.Holder] = i
			groups = append(groups, leaseHolderGroup{holder: lease.Holder})
		}
		groups[i].names = append(groups[i].names, lease.Name)
	}
	clauses := make([]string, 0, len(groups))
	for _, group := range groups {
		quoted := make([]string, len(group.names))
		for i, name := range group.names {
			quoted[i] = fmt.Sprintf("%q", name)
		}
		noun := "lease"
		if len(quoted) > 1 {
			noun = "leases"
		}
		clauses = append(clauses, fmt.Sprintf("%s (%s %s)", group.holder.String(), noun, strings.Join(quoted, ", ")))
	}
	return strings.Join(clauses, "; ")
}

// ProcessAlive reports whether pid is running. Signal 0 also succeeds for zombies, so the
// process state is checked where the platform exposes it; EPERM counts as alive.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if zombie, ok := core.PlatformProcessZombie(pid); ok {
		return !zombie
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
