package shared

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// A version claim stops two releases of the same version racing past preflight before
// either has pushed. It is scoped to the version, so different versions never collide.
const (
	// ReleaseVersionClaimTTL outlasts a slow multi-arch build; it is the only reclaim path for
	// a holder in another environment.
	ReleaseVersionClaimTTL             = 20 * time.Minute
	releaseVersionClaimRenewalInterval = ReleaseVersionClaimTTL / 3
)

func releaseVersionClaimScope(version string) string {
	return "release-version:" + strings.TrimSpace(version)
}

// releaseVersionClaimID is unique per attempt (pid): the same id would read as a renewal, not a conflict.
func releaseVersionClaimID(version string, pid int) string {
	return fmt.Sprintf("release-%s-%d", core.SanitizeForFilename(strings.TrimSpace(version)), pid)
}

// TakeReleaseVersionClaim takes the version's exclusive lease; separate so it can be tested with a fake clock.
func TakeReleaseVersionClaim(tenant, environment, version string, holder activity.EnvironmentActivityLeaseHolder, pid int, now time.Time) (activity.EnvironmentActivityLease, error) {
	return activity.TakeEnvironmentActivityLease(activity.TakeEnvironmentActivityLeaseParams{
		Tenant:      tenant,
		Environment: environment,
		Name:        "release " + strings.TrimSpace(version),
		ID:          releaseVersionClaimID(version, pid),
		PID:         pid,
		TTL:         ReleaseVersionClaimTTL,
		Exclusive:   true,
		Scope:       releaseVersionClaimScope(version),
		Holder:      holder,
		Now:         now,
	})
}

// ReleaseVersionClaimRefusalError names the version and holder for an operator-facing refusal.
func ReleaseVersionClaimRefusalError(version string, err error) error {
	var conflict *activity.EnvironmentActivityLeaseConflictError
	if !errors.As(err, &conflict) {
		return err
	}
	return fmt.Errorf("%s is already being released by %s — wait for it to finish; a holder that crashes or whose pod is replaced is reclaimed automatically on the next attempt",
		strings.TrimSpace(version), conflict.Holder.Holder.String())
}

// ClaimReleaseVersion takes the local exclusive lease and the repository-global claim before
// sync-remote mutates anything, and returns a func that drops both. No-op off-pod.
func ClaimReleaseVersion(ctx core.Context, spec ReleaseSpec, env func(string) string) (func(), error) {
	tenant, environment, ok := core.InjectedRuntimePodIdentity(env)
	if !ok {
		return func() {}, nil
	}
	ctx.Trace(fmt.Sprintf("release: claiming exclusive release lease for %s (tenant %s, environment %s)", spec.Version, tenant, environment))
	if ctx.DryRun {
		return func() {}, nil
	}

	holder := activity.EnvironmentActivityLeaseHolder{Orchestrator: strings.TrimSpace(env(OrchestratorIDEnvVar)), Tenant: tenant}
	pid := os.Getpid()

	// Read before taking our own local claim, so a live holder found is somebody else.
	localHolderAlive := func() bool {
		held, err := EnvironmentHasLiveReleaseHolder(tenant, environment, spec.Version, time.Now())
		if err != nil {
			// An unreadable store is not evidence the holder is gone.
			return true
		}
		return held
	}

	repoSHA, err := TakeReleaseRepoClaim(ctx, spec.ProjectRoot, environment, spec.Version, holder, time.Now(), localHolderAlive)
	if err != nil {
		return nil, err
	}
	repo := &releaseRepoClaimHandle{sha: repoSHA}

	if _, err := TakeReleaseVersionClaim(tenant, environment, spec.Version, holder, pid, time.Time{}); err != nil {
		releaseRepoClaimIfHeld(ctx, spec, repo)
		return nil, ReleaseVersionClaimRefusalError(spec.Version, err)
	}

	stop := make(chan struct{})
	var stopped sync.WaitGroup
	stopped.Add(1)
	go func() {
		defer stopped.Done()
		ticker := time.NewTicker(releaseVersionClaimRenewalInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				renewReleaseClaims(ctx, spec, tenant, environment, holder, pid, repo)
			}
		}
	}()

	scope := releaseVersionClaimScope(spec.Version)
	id := releaseVersionClaimID(spec.Version, pid)
	return func() {
		close(stop)
		stopped.Wait()
		_, _ = activity.ReleaseExclusiveEnvironmentActivityLease(tenant, environment, scope, id)
		releaseRepoClaimIfHeld(ctx, spec, repo)
	}, nil
}

// EnvironmentHasLiveReleaseHolder reports whether any holder has a live local claim on version.
// Reading reclaims dead holders; an unreadable store reports present.
func EnvironmentHasLiveReleaseHolder(tenant, environment, version string, now time.Time) (bool, error) {
	held, err := activity.LoadEnvironmentActivityLeases(tenant, environment, now)
	if err != nil {
		return false, err
	}
	scope := releaseVersionClaimScope(version)
	for _, lease := range held {
		if lease.Exclusive && lease.Scope == scope {
			return true, nil
		}
	}
	return false, nil
}

// releaseRepoClaimHandle is shared by the renewal goroutine and the cleanup func.
type releaseRepoClaimHandle struct {
	mu  sync.Mutex
	sha string
}

func (h *releaseRepoClaimHandle) get() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sha
}

func (h *releaseRepoClaimHandle) set(sha string) {
	h.mu.Lock()
	h.sha = sha
	h.mu.Unlock()
}

// renewReleaseClaims renews both claims best-effort; a failed renewal retries next tick.
func renewReleaseClaims(ctx core.Context, spec ReleaseSpec, tenant, environment string, holder activity.EnvironmentActivityLeaseHolder, pid int, repo *releaseRepoClaimHandle) {
	_, _ = TakeReleaseVersionClaim(tenant, environment, spec.Version, holder, pid, time.Time{})

	sha := repo.get()
	if sha == "" {
		return
	}
	if renewed, err := RenewReleaseRepoClaim(ctx, spec.ProjectRoot, environment, spec.Version, holder, time.Now(), sha); err == nil {
		repo.set(renewed)
	}
}

// releaseRepoClaimIfHeld drops the repository claim if one was taken (an inconclusive read leaves "").
func releaseRepoClaimIfHeld(ctx core.Context, spec ReleaseSpec, repo *releaseRepoClaimHandle) {
	if sha := repo.get(); sha != "" {
		_ = deleteReleaseRepoClaim(ctx, spec.ProjectRoot, spec.Version, sha)
	}
}
