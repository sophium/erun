package eruncommon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// HelmReleaseConcurrentDeployError is returned when an erun deploy invocation
// detects another in-flight deploy against the same release with different
// parameters. The caller should fail fast: retrying with the same parameters
// would still race the live deploy because they encode different intent.
type HelmReleaseConcurrentDeployError struct {
	ReleaseName       string
	Namespace         string
	KubernetesContext string
	OtherPID          int
	OtherStartedAt    time.Time
	OtherTenant       string
	OtherEnvironment  string
	OtherVersion      string
}

func (e *HelmReleaseConcurrentDeployError) Error() string {
	if e == nil {
		return ""
	}
	target := strings.TrimSpace(e.OtherTenant)
	if env := strings.TrimSpace(e.OtherEnvironment); env != "" {
		if target != "" {
			target += "/"
		}
		target += env
	}
	if target == "" {
		target = e.ReleaseName
	}
	if version := strings.TrimSpace(e.OtherVersion); version != "" {
		target += " " + version
	}
	return fmt.Sprintf(
		"another erun deploy is in progress for %s (release %q, namespace %q, context %q, pid=%d, started %s); refusing to run a conflicting helm upgrade",
		target,
		e.ReleaseName,
		e.Namespace,
		e.KubernetesContext,
		e.OtherPID,
		e.OtherStartedAt.UTC().Format(time.RFC3339),
	)
}

// HelmDeploySingleFlightOutcome describes what the dedup check decided.
type HelmDeploySingleFlightOutcome int

const (
	// HelmDeploySingleFlightProceed means the caller owns the in-flight
	// marker and should run the deploy, then Release the returned handle.
	HelmDeploySingleFlightProceed HelmDeploySingleFlightOutcome = iota
	// HelmDeploySingleFlightSkipDuplicate means an identical deploy is
	// already in flight; the caller should treat its invocation as a no-op
	// success.
	HelmDeploySingleFlightSkipDuplicate
)

// HelmDeploySingleFlightHandle owns the on-disk in-flight marker for a deploy.
// Release deletes the marker; it is safe to call on a nil or empty handle.
type HelmDeploySingleFlightHandle struct {
	path string
}

// Release removes the in-flight marker so subsequent deploys can proceed.
func (h *HelmDeploySingleFlightHandle) Release() {
	if h == nil || h.path == "" {
		return
	}
	_ = os.Remove(h.path)
}

type deployInflightRecord struct {
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
	ParamsHash  string    `json:"params_hash"`
	Tenant      string    `json:"tenant"`
	Environment string    `json:"environment"`
	Version     string    `json:"version"`
}

type helmDeploySingleFlightDeps struct {
	configDir func() (string, error)
	now       func() time.Time
	pid       func() int
	isAlive   func(int) bool
}

func (d helmDeploySingleFlightDeps) resolved() helmDeploySingleFlightDeps {
	if d.configDir == nil {
		d.configDir = ERunConfigDir
	}
	if d.now == nil {
		d.now = time.Now
	}
	if d.pid == nil {
		d.pid = os.Getpid
	}
	if d.isAlive == nil {
		d.isAlive = isProcessAlive
	}
	return d
}

const deployInflightDirName = "deploys"

// helmDeploySingleFlightMaxAge is the upper bound on how long a marker can
// reasonably represent a real in-flight deploy. Helm's default rollout
// timeout is 5 minutes; chains with multiple charts and large rollouts can
// stretch to ~10 minutes; anything beyond that is almost certainly a stale
// marker left behind by a CLI that died (commonly: a deploy CLI inside a
// runtime pod that was killed when the pod restarted, leaving a marker
// whose recorded PID is coincidentally alive in the new pod's PID
// namespace). Reclaiming on age covers that hole.
const helmDeploySingleFlightMaxAge = 15 * time.Minute

// AcquireHelmDeploySingleFlight tries to claim exclusive ownership of the
// helm release for the duration of a deploy. Behavior:
//
//   - Proceed + handle: caller should run the deploy and Release the handle
//     when finished (success or failure).
//   - SkipDuplicate: an identical deploy is already running; caller should
//     return success without invoking helm.
//   - HelmReleaseConcurrentDeployError: a different deploy is running for the
//     same release; caller should fail fast.
//
// In dry-run mode the function never touches the filesystem; it traces the
// would-be claim and returns Proceed with a nil handle so dry-run output
// remains a faithful preview of mutating intent without acquiring real state.
func AcquireHelmDeploySingleFlight(ctx Context, deploy HelmDeploySpec) (HelmDeploySingleFlightOutcome, *HelmDeploySingleFlightHandle, error) {
	return acquireHelmDeploySingleFlight(ctx, deploy, helmDeploySingleFlightDeps{})
}

func acquireHelmDeploySingleFlight(ctx Context, deploy HelmDeploySpec, deps helmDeploySingleFlightDeps) (HelmDeploySingleFlightOutcome, *HelmDeploySingleFlightHandle, error) {
	deps = deps.resolved()
	releaseKey := helmDeployReleaseKey(deploy)
	paramsHash := helmDeployParamsHash(deploy)

	configDir, err := deps.configDir()
	if err != nil || strings.TrimSpace(configDir) == "" {
		ctx.Trace("dedup: skip (config dir unavailable)")
		return HelmDeploySingleFlightProceed, nil, nil
	}
	deployDir := filepath.Join(configDir, deployInflightDirName)
	path := filepath.Join(deployDir, releaseKey+".json")

	if ctx.DryRun {
		return reportHelmDeploySingleFlightDryRun(ctx, deploy, deps, releaseKey, paramsHash, path)
	}

	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		ctx.Trace("dedup: skip (mkdir failed: " + err.Error() + ")")
		return HelmDeploySingleFlightProceed, nil, nil
	}

	record := deployInflightRecord{
		PID:         deps.pid(),
		StartedAt:   deps.now().UTC(),
		ParamsHash:  paramsHash,
		Tenant:      deploy.Tenant,
		Environment: deploy.Environment,
		Version:     deploy.Version,
	}

	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err == nil {
			handle, writeErr := writeFreshInflightMarker(ctx, f, path, record, releaseKey, paramsHash)
			if writeErr != nil {
				return HelmDeploySingleFlightProceed, nil, writeErr
			}
			return HelmDeploySingleFlightProceed, handle, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return HelmDeploySingleFlightProceed, nil, fmt.Errorf("acquire deploy in-flight marker: %w", err)
		}

		reclaim, outcome, recErr := reconcileExistingInflightMarker(ctx, deps, deploy, path, releaseKey, paramsHash)
		if reclaim {
			continue
		}
		return outcome, nil, recErr
	}
	return HelmDeploySingleFlightProceed, nil, fmt.Errorf("could not acquire deploy in-flight marker after retries")
}

// writeFreshInflightMarker cleans up the marker if writing or closing fails.
// A leftover marker carries our own still-alive PID, so it would block later
// deploys until the max-age reclaim rather than being seen as stale.
func writeFreshInflightMarker(ctx Context, f *os.File, path string, record deployInflightRecord, releaseKey, paramsHash string) (*HelmDeploySingleFlightHandle, error) {
	if writeErr := writeInflightRecord(f, record); writeErr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write deploy in-flight marker: %w", writeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close deploy in-flight marker: %w", closeErr)
	}
	ctx.Trace(fmt.Sprintf("dedup: claim (release=%s, hash=%s, pid=%d)", releaseKey, paramsHash, record.PID))
	return &HelmDeploySingleFlightHandle{path: path}, nil
}

// removeInflightMarker tolerates an already-absent marker so a concurrent reclaim
// does not surface as an error.
func removeInflightMarker(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// reconcileExistingInflightMarker decides how to handle the marker that blocked
// our claim. A true first return means it reclaimed the marker and the caller
// should retry the exclusive create.
func reconcileExistingInflightMarker(ctx Context, deps helmDeploySingleFlightDeps, deploy HelmDeploySpec, path, releaseKey, paramsHash string) (bool, HelmDeploySingleFlightOutcome, error) {
	existing, readErr := readInflightRecord(path)
	if readErr != nil {
		ctx.Trace("dedup: replacing unreadable in-flight marker (" + readErr.Error() + ")")
		if rmErr := removeInflightMarker(path); rmErr != nil {
			return false, HelmDeploySingleFlightProceed, fmt.Errorf("remove unreadable in-flight marker: %w", rmErr)
		}
		return true, HelmDeploySingleFlightProceed, nil
	}
	if !deps.isAlive(existing.PID) {
		ctx.Trace(fmt.Sprintf("dedup: reclaim (release=%s, prior pid=%d is dead)", releaseKey, existing.PID))
		if rmErr := removeInflightMarker(path); rmErr != nil {
			return false, HelmDeploySingleFlightProceed, fmt.Errorf("remove stale in-flight marker: %w", rmErr)
		}
		return true, HelmDeploySingleFlightProceed, nil
	}
	if !existing.StartedAt.IsZero() && deps.now().Sub(existing.StartedAt) > helmDeploySingleFlightMaxAge {
		ctx.Trace(fmt.Sprintf("dedup: reclaim (release=%s, marker age %s exceeds max %s)", releaseKey, deps.now().Sub(existing.StartedAt).Round(time.Second), helmDeploySingleFlightMaxAge))
		if rmErr := removeInflightMarker(path); rmErr != nil {
			return false, HelmDeploySingleFlightProceed, fmt.Errorf("remove aged-out in-flight marker: %w", rmErr)
		}
		return true, HelmDeploySingleFlightProceed, nil
	}
	if existing.ParamsHash == paramsHash {
		ctx.Trace(fmt.Sprintf("dedup: skip (release=%s, hash=%s, pid=%d already running identical deploy)", releaseKey, paramsHash, existing.PID))
		return false, HelmDeploySingleFlightSkipDuplicate, nil
	}
	return false, HelmDeploySingleFlightProceed, &HelmReleaseConcurrentDeployError{
		ReleaseName:       deploy.ReleaseName,
		Namespace:         deploy.Namespace,
		KubernetesContext: deploy.KubernetesContext,
		OtherPID:          existing.PID,
		OtherStartedAt:    existing.StartedAt,
		OtherTenant:       existing.Tenant,
		OtherEnvironment:  existing.Environment,
		OtherVersion:      existing.Version,
	}
}

// reportHelmDeploySingleFlightDryRun mirrors the dedup decision a real run would
// make but never mutates the on-disk marker, so dry-run stays side-effect free.
func reportHelmDeploySingleFlightDryRun(ctx Context, deploy HelmDeploySpec, deps helmDeploySingleFlightDeps, releaseKey, paramsHash, path string) (HelmDeploySingleFlightOutcome, *HelmDeploySingleFlightHandle, error) {
	existing, readErr := readInflightRecord(path)
	if errors.Is(readErr, os.ErrNotExist) {
		ctx.Trace(fmt.Sprintf("dedup: ready (release=%s, hash=%s)", releaseKey, paramsHash))
		return HelmDeploySingleFlightProceed, nil, nil
	}
	if readErr != nil {
		ctx.Trace("dedup: ready (release=" + releaseKey + ", hash=" + paramsHash + ", existing marker unreadable: " + readErr.Error() + ")")
		return HelmDeploySingleFlightProceed, nil, nil
	}
	if !deps.isAlive(existing.PID) {
		ctx.Trace(fmt.Sprintf("dedup: would reclaim (release=%s, prior pid=%d is dead)", releaseKey, existing.PID))
		return HelmDeploySingleFlightProceed, nil, nil
	}
	if existing.ParamsHash == paramsHash {
		ctx.Trace(fmt.Sprintf("dedup: would skip (release=%s, hash=%s, pid=%d already running identical deploy)", releaseKey, paramsHash, existing.PID))
		return HelmDeploySingleFlightSkipDuplicate, nil, nil
	}
	return HelmDeploySingleFlightProceed, nil, &HelmReleaseConcurrentDeployError{
		ReleaseName:       deploy.ReleaseName,
		Namespace:         deploy.Namespace,
		KubernetesContext: deploy.KubernetesContext,
		OtherPID:          existing.PID,
		OtherStartedAt:    existing.StartedAt,
		OtherTenant:       existing.Tenant,
		OtherEnvironment:  existing.Environment,
		OtherVersion:      existing.Version,
	}
}

func helmDeployReleaseKey(deploy HelmDeploySpec) string {
	parts := []string{deploy.KubernetesContext, deploy.Namespace, deploy.ReleaseName}
	for i, p := range parts {
		parts[i] = sanitizeForFilename(p)
	}
	return strings.Join(parts, "-")
}

func sanitizeForFilename(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

func helmDeployParamsHash(deploy HelmDeploySpec) string {
	cmd := deploy.command()
	h := sha256.New()
	h.Write([]byte(cmd.Name))
	for _, arg := range cmd.Args {
		h.Write([]byte{0})
		h.Write([]byte(arg))
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

func writeInflightRecord(f *os.File, record deployInflightRecord) error {
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(record)
}

func readInflightRecord(path string) (deployInflightRecord, error) {
	var record deployInflightRecord
	data, err := os.ReadFile(path)
	if err != nil {
		return record, err
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return record, err
	}
	return record, nil
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		// Can't determine liveness. On Windows os.FindProcess calls OpenProcess,
		// which an endpoint-security agent denies — returning "dead" here would
		// make a LIVE concurrent deploy look finished and silently defeat the
		// single-flight guard, so rapid/duplicate deploys stop being deduped.
		// Assume alive instead; a genuinely-crashed marker is still reclaimed by
		// the max-age fallback in reconcileExistingInflightMarker. (Unix
		// FindProcess never errors, so this path is Windows-only in practice.)
		return true
	}
	signalErr := proc.Signal(syscall.Signal(0))
	if signalErr == nil {
		return true
	}
	// On darwin (Go 1.23+) `(*os.Process).Signal` returns os.ErrProcessDone
	// for a dead PID instead of surfacing the underlying ESRCH, so a check
	// against ESRCH alone leaves the marker stuck until the max-age fallback.
	if errors.Is(signalErr, syscall.ESRCH) || errors.Is(signalErr, os.ErrProcessDone) {
		return false
	}
	// EPERM and other errors imply the process exists but we can't signal it.
	return true
}
