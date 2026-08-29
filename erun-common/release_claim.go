package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// A release used to have nothing standing in for "I am releasing this
// version" the way wip:<orchestrator> labels stand in for "I am working this
// issue": every preflight check (worktree, VERSION, local tag, remote tag,
// disk headroom) passed correctly for two orchestrators racing the same
// version, because neither had pushed anything yet. The loser found out ~11
// minutes later holding a local tag that disagreed with the one the winner
// had already published.
//
// The exclusive activity lease is the existing primitive that already
// answers this for a worktree (root AGENTS.md's "wip: labels" precedent,
// generalized): environment-side, visible, and reconciled against its
// holder. Scoping the claim to the version being released, rather than the
// whole worktree, means two releases of different versions in the same
// environment never collide.
const (
	// releaseVersionClaimTTL is generous relative to a real release's build
	// time (multi-arch image builds routinely run past ten minutes) so a
	// live release is never mistaken for an abandoned one. Renewal keeps a
	// running release well inside it; a release that stops renewing —
	// crashed, or its pod was replaced — is reclaimed once it lapses.
	releaseVersionClaimTTL = 20 * time.Minute
	// releaseVersionClaimRenewalInterval keeps the renewal comfortably
	// inside the TTL, the same margin the job-lease heartbeat uses.
	releaseVersionClaimRenewalInterval = releaseVersionClaimTTL / 3
)

// releaseVersionClaimScope names the exclusive lease scope a release of
// version claims, so two releases of the same version collide and two
// releases of different versions in the same environment never do.
func releaseVersionClaimScope(version string) string {
	return "release-version:" + strings.TrimSpace(version)
}

// releaseVersionClaimID is unique per attempt (the pid of the process making
// it), not per version: the exclusive lease treats a second take with the
// *same* id as this same claim renewing rather than a conflict, so two
// different releases of the same version must never share one. It stays
// stable across one release's own renewal ticks because those reuse the pid
// that made the original claim.
func releaseVersionClaimID(version string, pid int) string {
	return fmt.Sprintf("release-%s-%d", sanitizeForFilename(strings.TrimSpace(version)), pid)
}

// takeReleaseVersionClaim is the thin wrapper over the exclusive activity
// lease that names the claim's shape, kept separate from claimReleaseVersion
// so the refuse/reclaim behavior can be exercised directly against a
// controlled clock rather than through a real release run.
func takeReleaseVersionClaim(tenant, environment, version string, holder EnvironmentActivityLeaseHolder, pid int, now time.Time) (EnvironmentActivityLease, error) {
	return TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant:      tenant,
		Environment: environment,
		Name:        "release " + strings.TrimSpace(version),
		ID:          releaseVersionClaimID(version, pid),
		PID:         pid,
		TTL:         releaseVersionClaimTTL,
		Exclusive:   true,
		Scope:       releaseVersionClaimScope(version),
		Holder:      holder,
		Now:         now,
	})
}

// releaseVersionClaimRefusalError turns a raw lease conflict into the
// operator-facing refusal: it names the version and the holder, and says
// what to do — wait, or nothing, since a dead holder reclaims itself.
func releaseVersionClaimRefusalError(version string, err error) error {
	var conflict *EnvironmentActivityLeaseConflictError
	if !errors.As(err, &conflict) {
		return err
	}
	return fmt.Errorf("%s is already being released by %s — wait for it to finish; a holder that crashes or whose pod is replaced is reclaimed automatically on the next attempt",
		strings.TrimSpace(version), conflict.Holder.Holder.String())
}

// claimReleaseVersion takes both claims that stand in for "I am releasing
// this version" — the local exclusive lease (environment-scoped: keeps this
// environment from idling out mid-release and is visible to `erun` lease
// listings) and the repository-global claim on a remote ref (release_repo_claim.go:
// the one that actually stops two orchestrators driving different
// environments from racing the same version) — before sync-remote does
// anything the loser of a race would have to unwind. It returns a release
// func to defer, which stops renewing and drops both claims; call it whether
// the release succeeds or fails.
//
// The claim only applies inside a runtime pod: injectedRuntimePodIdentity
// resolves ERUN_TENANT/ERUN_ENVIRONMENT, which only the runtime chart sets.
// Off-pod (a developer's own laptop) there is exactly one caller by
// construction, so this is a no-op — refusing a solo release against itself
// would be a false positive, not a safety net.
func claimReleaseVersion(ctx Context, spec ReleaseSpec, env func(string) string) (func(), error) {
	tenant, environment, ok := injectedRuntimePodIdentity(env)
	if !ok {
		return func() {}, nil
	}
	ctx.Trace(fmt.Sprintf("release: claiming exclusive release lease for %s (tenant %s, environment %s)", spec.Version, tenant, environment))
	if ctx.DryRun {
		return func() {}, nil
	}

	holder := EnvironmentActivityLeaseHolder{Orchestrator: strings.TrimSpace(env("ERUN_ORCHESTRATOR_ID")), Tenant: tenant}
	pid := os.Getpid()

	repoSHA, err := takeReleaseRepoClaim(ctx, spec.ProjectRoot, spec.Version, holder, pid, time.Now())
	if err != nil {
		return nil, err
	}
	repo := &releaseRepoClaimHandle{sha: repoSHA}

	if _, err := takeReleaseVersionClaim(tenant, environment, spec.Version, holder, pid, time.Time{}); err != nil {
		releaseRepoClaimIfHeld(ctx, spec, repo)
		return nil, releaseVersionClaimRefusalError(spec.Version, err)
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
		_ = ReleaseExclusiveEnvironmentActivityLease(tenant, environment, scope, id)
		releaseRepoClaimIfHeld(ctx, spec, repo)
	}, nil
}

// releaseRepoClaimHandle tracks the repository-global claim's current sha
// across the renewal goroutine and the release func's cleanup, both of which
// run concurrently with each other for the whole life of the release.
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

// renewReleaseClaims is one renewal tick for both claims. The repository
// claim is best-effort, matching the local lease's own renewal: a failed
// renewal keeps the last-known sha and tries again next tick rather than
// tearing down a release over a transient push failure.
func renewReleaseClaims(ctx Context, spec ReleaseSpec, tenant, environment string, holder EnvironmentActivityLeaseHolder, pid int, repo *releaseRepoClaimHandle) {
	_, _ = takeReleaseVersionClaim(tenant, environment, spec.Version, holder, pid, time.Time{})

	sha := repo.get()
	if sha == "" {
		return
	}
	if renewed, err := renewReleaseRepoClaim(ctx, spec.ProjectRoot, spec.Version, holder, pid, time.Now(), sha); err == nil {
		repo.set(renewed)
	}
}

// releaseRepoClaimIfHeld drops the repository-global claim if this run ever
// actually took one (an inconclusive remote read leaves repo holding "").
func releaseRepoClaimIfHeld(ctx Context, spec ReleaseSpec, repo *releaseRepoClaimHandle) {
	if sha := repo.get(); sha != "" {
		_ = deleteReleaseRepoClaim(ctx, spec.ProjectRoot, spec.Version, sha)
	}
}
