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

const (
	// DefaultEnvironmentActivityLeaseTTL is long enough that a job renewing on
	// the monitor's cadence never lapses mid-run, short enough that an
	// uninstrumented crash releases the environment within one idle window.
	DefaultEnvironmentActivityLeaseTTL = 15 * time.Minute

	// EnvironmentActivityLeaseMaxLifetime is the hard ceiling a renewal cannot
	// push past. Without it a holder that renews forever — or a wrapper looping
	// on a hung job — would keep an environment awake indefinitely, which is the
	// failure this whole mechanism exists to make impossible.
	EnvironmentActivityLeaseMaxLifetime = 12 * time.Hour

	// environmentActivityLeaseMarker is the idle marker leases report through, so
	// a held lease blocks idle-stop by the same predicate every other signal uses.
	environmentActivityLeaseMarker = "lease"

	environmentActivityLeaseDirName = "leases"
)

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
}

// normalize fills the defaults and rejects the inputs that would produce a
// lease nobody can act on: one that names no work, or one that never lapses.
func (p TakeEnvironmentActivityLeaseParams) normalize() (TakeEnvironmentActivityLeaseParams, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return p, fmt.Errorf("lease name is required")
	}
	id, err := resolveEnvironmentActivityLeaseID(p.ID, p.Name)
	if err != nil {
		return p, err
	}
	p.ID = id
	if p.TTL == 0 {
		p.TTL = DefaultEnvironmentActivityLeaseTTL
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
	id, name, now := params.ID, params.Name, params.Now
	dir, err := environmentActivityLeaseDir(params.Tenant, params.Environment)
	if err != nil {
		return EnvironmentActivityLease{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return EnvironmentActivityLease{}, err
	}

	path := filepath.Join(dir, id+".json")
	lease := EnvironmentActivityLease{ID: id, Name: name, PID: params.PID, StartedAt: now}
	// Only a lease still held is renewed. Reusing an id whose previous holder
	// is gone starts a fresh claim, or the new lease would inherit a start far
	// enough in the past to be dead on arrival.
	if existing, err := loadEnvironmentActivityLease(path); err == nil && environmentActivityLeaseHeld(existing, now, processAlive) {
		lease.StartedAt = existing.StartedAt
		lease.RenewedAt = now
	}
	lease.ExpiresAt = capEnvironmentActivityLeaseExpiry(lease.StartedAt, now.Add(params.TTL))

	data, err := json.MarshalIndent(lease, "", "  ")
	if err != nil {
		return EnvironmentActivityLease{}, err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return EnvironmentActivityLease{}, err
	}
	return lease, nil
}

// ReleaseEnvironmentActivityLease drops a lease. Idempotent: releasing a lease
// that already expired or was never taken is success, so a wrapper's exit trap
// never fails a job that already finished cleanly.
func ReleaseEnvironmentActivityLease(tenant, environment, id string) error {
	resolved, err := resolveEnvironmentActivityLeaseID(id, id)
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
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
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
	sort.Slice(held, func(i, j int) bool {
		if held[i].Name != held[j].Name {
			return held[i].Name < held[j].Name
		}
		return held[i].ID < held[j].ID
	})
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

func resolveEnvironmentActivityLeaseID(id, fallback string) (string, error) {
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

func environmentActivityLeaseDir(tenant, environment string) (string, error) {
	dir, err := EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, environmentActivityLeaseDirName), nil
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
