package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
)

// HelmReleaseConcurrentDeployError reports an in-flight deploy of the same
// release with different parameters; retrying would still race it.
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

// HelmDeploySingleFlightOutcome is the dedup decision.
type HelmDeploySingleFlightOutcome int

const (
	// HelmDeploySingleFlightProceed means the caller owns the marker and must Release it.
	HelmDeploySingleFlightProceed HelmDeploySingleFlightOutcome = iota
	// HelmDeploySingleFlightSkipDuplicate means an identical deploy is in flight.
	HelmDeploySingleFlightSkipDuplicate
)

// HelmDeploySingleFlightHandle owns the on-disk in-flight marker.
type HelmDeploySingleFlightHandle struct {
	path string
}

// Release removes the marker; safe on a nil handle.
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
		d.configDir = config.ERunConfigDir
	}
	if d.now == nil {
		d.now = time.Now
	}
	if d.pid == nil {
		d.pid = os.Getpid
	}
	if d.isAlive == nil {
		d.isAlive = core.IsProcessAlive
	}
	return d
}

func (d helmDeploySingleFlightDeps) usableConfigDir() (string, bool) {
	configDir, err := d.configDir()
	return configDir, err == nil && strings.TrimSpace(configDir) != ""
}

const deployInflightDirName = "deploys"

// helmDeploySingleFlightMaxAge reclaims markers whose recorded PID happens to be
// alive in a restarted pod's PID namespace.
const helmDeploySingleFlightMaxAge = 15 * time.Minute

// AcquireHelmDeploySingleFlight claims the release for a deploy. Dry-run never
// touches the filesystem and returns a nil handle.
func AcquireHelmDeploySingleFlight(ctx core.Context, deploy seam.HelmDeploySpec) (HelmDeploySingleFlightOutcome, *HelmDeploySingleFlightHandle, error) {
	return acquireHelmDeploySingleFlight(ctx, deploy, helmDeploySingleFlightDeps{})
}

func acquireHelmDeploySingleFlight(ctx core.Context, deploy seam.HelmDeploySpec, deps helmDeploySingleFlightDeps) (HelmDeploySingleFlightOutcome, *HelmDeploySingleFlightHandle, error) {
	deps = deps.resolved()
	releaseKey := helmDeployReleaseKey(deploy)
	paramsHash := helmDeployParamsHash(deploy)

	configDir, ok := deps.usableConfigDir()
	if !ok {
		ctx.Trace("dedup: skip (config dir unavailable)")
		return HelmDeploySingleFlightProceed, nil, nil
	}
	deployDir := filepath.Join(configDir, deployInflightDirName)
	path := filepath.Join(deployDir, releaseKey+".json")

	if ctx.DryRun {
		return reportHelmDeploySingleFlightDryRun(ctx, deploy, deps, releaseKey, paramsHash, path)
	}

	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		ctx.Trace(fmt.Sprintf("dedup: skip (mkdir failed: %v)", err))
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

// A leftover marker carries our live PID and would block deploys until max age.
func writeFreshInflightMarker(ctx core.Context, f *os.File, path string, record deployInflightRecord, releaseKey, paramsHash string) (*HelmDeploySingleFlightHandle, error) {
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

func removeInflightMarker(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// inflightMarkerFate is shared by the mutating and dry-run paths so they cannot
// drift.
type inflightMarkerFate int

const (
	inflightMarkerFateReclaimDead inflightMarkerFate = iota
	inflightMarkerFateReclaimAged
	inflightMarkerFateSkipDuplicate
	inflightMarkerFateConcurrent
)

func decideInflightMarkerFate(deps helmDeploySingleFlightDeps, existing deployInflightRecord, paramsHash string) inflightMarkerFate {
	if !deps.isAlive(existing.PID) {
		return inflightMarkerFateReclaimDead
	}
	if !existing.StartedAt.IsZero() && deps.now().Sub(existing.StartedAt) > helmDeploySingleFlightMaxAge {
		return inflightMarkerFateReclaimAged
	}
	if existing.ParamsHash == paramsHash {
		return inflightMarkerFateSkipDuplicate
	}
	return inflightMarkerFateConcurrent
}

func concurrentDeployError(deploy seam.HelmDeploySpec, existing deployInflightRecord) *HelmReleaseConcurrentDeployError {
	return &HelmReleaseConcurrentDeployError{
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

// reconcileExistingInflightMarker returns true when it reclaimed the marker and
// the create should be retried.
func reconcileExistingInflightMarker(ctx core.Context, deps helmDeploySingleFlightDeps, deploy seam.HelmDeploySpec, path, releaseKey, paramsHash string) (bool, HelmDeploySingleFlightOutcome, error) {
	existing, readErr := readInflightRecord(path)
	if readErr != nil {
		ctx.Trace("dedup: replacing unreadable in-flight marker (" + readErr.Error() + ")")
		if rmErr := removeInflightMarker(path); rmErr != nil {
			return false, HelmDeploySingleFlightProceed, fmt.Errorf("remove unreadable in-flight marker: %w", rmErr)
		}
		return true, HelmDeploySingleFlightProceed, nil
	}
	switch decideInflightMarkerFate(deps, existing, paramsHash) {
	case inflightMarkerFateReclaimDead:
		ctx.Trace(fmt.Sprintf("dedup: reclaim (release=%s, prior pid=%d is dead)", releaseKey, existing.PID))
		if rmErr := removeInflightMarker(path); rmErr != nil {
			return false, HelmDeploySingleFlightProceed, fmt.Errorf("remove stale in-flight marker: %w", rmErr)
		}
		return true, HelmDeploySingleFlightProceed, nil
	case inflightMarkerFateReclaimAged:
		ctx.Trace(fmt.Sprintf("dedup: reclaim (release=%s, marker age %s exceeds max %s)", releaseKey, deps.now().Sub(existing.StartedAt).Round(time.Second), helmDeploySingleFlightMaxAge))
		if rmErr := removeInflightMarker(path); rmErr != nil {
			return false, HelmDeploySingleFlightProceed, fmt.Errorf("remove aged-out in-flight marker: %w", rmErr)
		}
		return true, HelmDeploySingleFlightProceed, nil
	case inflightMarkerFateSkipDuplicate:
		ctx.Trace(fmt.Sprintf("dedup: skip (release=%s, hash=%s, pid=%d already running identical deploy)", releaseKey, paramsHash, existing.PID))
		return false, HelmDeploySingleFlightSkipDuplicate, nil
	case inflightMarkerFateConcurrent:
	}
	return false, HelmDeploySingleFlightProceed, concurrentDeployError(deploy, existing)
}

func reportHelmDeploySingleFlightDryRun(ctx core.Context, deploy seam.HelmDeploySpec, deps helmDeploySingleFlightDeps, releaseKey, paramsHash, path string) (HelmDeploySingleFlightOutcome, *HelmDeploySingleFlightHandle, error) {
	existing, readErr := readInflightRecord(path)
	if errors.Is(readErr, os.ErrNotExist) {
		ctx.Trace(fmt.Sprintf("dedup: ready (release=%s, hash=%s)", releaseKey, paramsHash))
		return HelmDeploySingleFlightProceed, nil, nil
	}
	if readErr != nil {
		ctx.Trace(fmt.Sprintf("dedup: ready (release=%s, hash=%s, existing marker unreadable: %v)", releaseKey, paramsHash, readErr))
		return HelmDeploySingleFlightProceed, nil, nil
	}
	switch decideInflightMarkerFate(deps, existing, paramsHash) {
	case inflightMarkerFateReclaimDead:
		ctx.Trace(fmt.Sprintf("dedup: would reclaim (release=%s, prior pid=%d is dead)", releaseKey, existing.PID))
		return HelmDeploySingleFlightProceed, nil, nil
	case inflightMarkerFateReclaimAged:
		ctx.Trace(fmt.Sprintf("dedup: would reclaim (release=%s, marker age %s exceeds max %s)", releaseKey, deps.now().Sub(existing.StartedAt).Round(time.Second), helmDeploySingleFlightMaxAge))
		return HelmDeploySingleFlightProceed, nil, nil
	case inflightMarkerFateSkipDuplicate:
		ctx.Trace(fmt.Sprintf("dedup: would skip (release=%s, hash=%s, pid=%d already running identical deploy)", releaseKey, paramsHash, existing.PID))
		return HelmDeploySingleFlightSkipDuplicate, nil, nil
	case inflightMarkerFateConcurrent:
	}
	return HelmDeploySingleFlightProceed, nil, concurrentDeployError(deploy, existing)
}

func helmDeployReleaseKey(deploy seam.HelmDeploySpec) string {
	parts := []string{deploy.KubernetesContext, deploy.Namespace, deploy.ReleaseName}
	for i, p := range parts {
		parts[i] = core.SanitizeForFilename(p)
	}
	return strings.Join(parts, "-")
}

func helmDeployParamsHash(deploy seam.HelmDeploySpec) string {
	cmd := helmDeployCommand(deploy)
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
