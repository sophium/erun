package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/releaseexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// ReleaseQueueRepository is the persistence behind the per-tenant serial queue.
type ReleaseQueueRepository interface {
	Create(ctx context.Context, release model.Release) (model.Release, error)
	Get(ctx context.Context, releaseID string) (model.Release, error)
	FindByCommit(ctx context.Context, commitID string) (model.Release, error)
	ClaimNext(ctx context.Context, window repository.ClaimWindow) (model.Release, bool, error)
	Requeue(ctx context.Context, releaseID string) (model.Release, error)
	RecordOutcome(ctx context.Context, releaseID string, outcome repository.ReleaseOutcome) error
	ExpireStale(ctx context.Context, staleAfter time.Duration, reason string) (int, error)
}

// ReleaseRunner runs one release attempt to a terminal result — satisfied by the
// releaseexec Job launcher, which the durable workflow supplies.
type ReleaseRunner interface {
	Run(ctx context.Context, params releaseexec.ReleaseJobParams) (releaseexec.Result, error)
}

// ReleaseBuildRecorder records the build a release produced and moves the review
// with it — the existing build service, which owns both halves.
type ReleaseBuildRecorder interface {
	Create(ctx context.Context, build model.Build) (model.Build, error)
}

const (
	// A release that has not reported in this long is treated as abandoned: the
	// window is wider than the release Job's own three-hour deadline, so a run
	// that could still be live never has its slot taken away.
	releaseStaleAfter = 4 * time.Hour
	// releaseCooldown is the minimum spacing between one tenant's consecutive
	// releases. A release is minutes of cluster time, so a trigger stuck in a loop
	// would otherwise spend the tenant's capacity on back-to-back runs; against a
	// real release's duration the wait costs nothing.
	releaseCooldown = 60 * time.Second
	// cooldownDispatchSlack puts the follow-on dispatch just past the cooldown
	// boundary rather than exactly on it, so the claim it makes is not refused by
	// the window it waited out.
	cooldownDispatchSlack = 2 * time.Second
	// maxDispatchPerPass caps how many releases one dispatch pass may start. The
	// per-tenant invariant already allows only one each, so this bounds the fan-out
	// across tenants and keeps a queue full of work from starting an unbounded
	// number of Jobs in one pass.
	maxDispatchPerPass = 4
	// staleReleaseReason is what an abandoned release records. It says the run
	// stopped reporting rather than that the release failed, because what happened
	// to the Job is not knowable from here.
	staleReleaseReason = "the control plane running this release stopped reporting before it reached a terminal state; re-trigger the commit to run it again"
)

// ReleaseRequest is one trigger: the merge commit to release, the branch it
// landed on, and the review that earned it.
type ReleaseRequest struct {
	ReviewID     string
	TargetBranch string
	CommitID     string
}

// EnqueueResult reports what the trigger did. AlreadyReleased is the first-class
// "this commit is already released" answer: the row names the version that was
// minted for it, and nothing new was queued.
type EnqueueResult struct {
	Release         model.Release
	Created         bool
	Requeued        bool
	AlreadyReleased bool
}

// ReleaseService owns the release queue's workflow: enqueueing a trigger
// idempotently, claiming the next release for a tenant, and running one attempt
// to a recorded terminal state.
type ReleaseService struct {
	releases ReleaseQueueRepository
	builds   ReleaseBuildRecorder
	runner   ReleaseRunner
	// cooldownWait is how long a finished release waits before coming back for the
	// next one. It clears the cooldown its own terminal write opened, with enough
	// slack to be past the boundary rather than exactly on it.
	cooldownWait time.Duration
}

func NewReleaseService(releases ReleaseQueueRepository, builds ReleaseBuildRecorder, runner ReleaseRunner) *ReleaseService {
	return &ReleaseService{
		releases:     releases,
		builds:       builds,
		runner:       runner,
		cooldownWait: releaseCooldown + cooldownDispatchSlack,
	}
}

// Enqueue records a release request, or returns the one this merge commit
// already has. Minting a second version for one merge commit is the worst thing
// this queue could do, so the commit — not the request — is the identity: an
// already-released commit is reported back untouched, an in-flight one is
// reported as-is, and only a failed attempt is queued again.
func (s *ReleaseService) Enqueue(ctx context.Context, request ReleaseRequest) (EnqueueResult, error) {
	commit := strings.TrimSpace(request.CommitID)
	branch := strings.TrimSpace(request.TargetBranch)
	if commit == "" || branch == "" {
		return EnqueueResult{}, repository.ErrInvalidInput
	}

	existing, err := s.releases.FindByCommit(ctx, commit)
	switch {
	case err == nil:
		return s.reuse(ctx, existing)
	case errors.Is(err, repository.ErrNotFound):
	default:
		return EnqueueResult{}, err
	}

	created, err := s.releases.Create(ctx, model.Release{
		ReviewID:     strings.TrimSpace(request.ReviewID),
		TargetBranch: branch,
		CommitID:     commit,
	})
	if err != nil {
		// A concurrent trigger for the same commit loses the insert on the
		// per-commit uniqueness contract; its row is the answer, not the error.
		if raced, findErr := s.releases.FindByCommit(ctx, commit); findErr == nil {
			return s.reuse(ctx, raced)
		}
		return EnqueueResult{}, err
	}
	return EnqueueResult{Release: created, Created: true}, nil
}

// reuse decides what an existing row for this commit means for a fresh trigger.
func (s *ReleaseService) reuse(ctx context.Context, existing model.Release) (EnqueueResult, error) {
	switch existing.Status {
	case model.ReleaseStatusReleased:
		return EnqueueResult{Release: existing, AlreadyReleased: true}, nil
	case model.ReleaseStatusFailed:
		requeued, err := s.releases.Requeue(ctx, existing.ReleaseID)
		if err != nil {
			return EnqueueResult{}, err
		}
		return EnqueueResult{Release: requeued, Requeued: true}, nil
	default:
		return EnqueueResult{Release: existing}, nil
	}
}

// Dispatch starts every release that may run right now and reports how many it
// started. It is the only place the queue hands work out, so the two bounds live
// here: a claim refuses while that tenant already has one in flight or is inside
// its cooldown, and the pass itself stops at maxDispatchPerPass so a queue full
// of work cannot launch an unbounded number of Jobs in one go.
//
// Called after a trigger and after each release finishes, which is what drains
// the queue without a poller.
func (s *ReleaseService) Dispatch(ctx context.Context, start func(model.Release) error) (int, error) {
	if expired, err := s.releases.ExpireStale(ctx, releaseStaleAfter, staleReleaseReason); err != nil {
		return 0, err
	} else if expired > 0 {
		log.Printf("erun api release queue: %d release(s) stopped reporting past %s and were failed so the queue could move on", expired, releaseStaleAfter)
	}

	started := 0
	for started < maxDispatchPerPass {
		release, claimed, err := s.releases.ClaimNext(ctx, repository.ClaimWindow{Cooldown: releaseCooldown})
		if err != nil {
			return started, err
		}
		if !claimed {
			return started, nil
		}
		if err := start(release); err != nil {
			// The claim already moved the row to running, so a start that never
			// happened would hold the tenant's only slot until the stale window
			// expires it. Hand it back now, naming why.
			return started, s.fail(ctx, release, "the release could not be started: "+err.Error(), err)
		}
		started++
	}
	// A cap that silently drops work reads as "the queue is empty" when it is not.
	log.Printf("erun api release queue: dispatch stopped at its cap of %d started releases; the rest stay queued until the next dispatch", maxDispatchPerPass)
	return started, nil
}

// DispatchAfterCooldown hands the freed slot on once the cooldown the finished
// release itself opened has passed. Without it the queue stalls: a release that
// finishes dispatches into its own cooldown, the claim is refused, and — with no
// poller — nothing comes back for the work still queued. Waiting here is what
// drains the queue without one.
func (s *ReleaseService) DispatchAfterCooldown(ctx context.Context, start func(model.Release) error) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(s.cooldownWait):
	}
	return s.Dispatch(ctx, start)
}

// Get reads one release.
func (s *ReleaseService) Get(ctx context.Context, releaseID string) (model.Release, error) {
	return s.releases.Get(ctx, releaseID)
}

// Run executes one claimed release attempt and records what it did. The release
// row is already `running` — the claim is what put it there — so every path out
// of here has to leave it terminal, or the tenant's only in-flight slot stays
// held until the stale window expires it.
func (s *ReleaseService) Run(ctx context.Context, release model.Release, params releaseexec.ReleaseJobParams) error {
	if s.runner == nil {
		reason := "this control plane has no release executor configured, so the release was queued but cannot be run here"
		return s.fail(ctx, release, reason, errors.New(reason))
	}
	result, runErr := s.runner.Run(ctx, params)
	if runErr != nil {
		return s.fail(ctx, release, runErr.Error(), runErr)
	}
	if result.Outcome != releaseexec.OutcomeSucceeded {
		return s.fail(ctx, release, releaseFailureReason(params, result), fmt.Errorf("release job outcome %q", result.Outcome))
	}
	version := strings.TrimSpace(result.Version)
	if version == "" {
		// A release that exits 0 has published; one that will not say what it
		// published leaves nothing able to name the version, which is not a success
		// the control plane can record.
		reason := "the release job exited successfully but never reported the version it published, so nothing can name what was released"
		return s.fail(ctx, release, reason, errors.New(reason))
	}

	// The version is public by the time the Job exits 0, so the build and the
	// released status record something that has already happened. A write that
	// failed here would leave the row unable to name a version the registry is
	// already serving, which is exactly what recovery needs.
	buildID, buildErr := s.recordBuild(ctx, release, version)
	if buildErr != nil {
		log.Printf("erun api release: recording the build for release=%q version=%q did not persist: %v", release.ReleaseID, version, buildErr)
	}
	return s.releases.RecordOutcome(ctx, release.ReleaseID, repository.ReleaseOutcome{
		Status:  model.ReleaseStatusReleased,
		Version: version,
		BuildID: buildID,
	})
}

// recordBuild writes the release's build against the review that earned it,
// which is what moves the review with the build result. A release triggered by a
// branch merge rather than a review has nothing to record against.
func (s *ReleaseService) recordBuild(ctx context.Context, release model.Release, version string) (string, error) {
	if release.ReviewID == "" || s.builds == nil {
		return "", nil
	}
	build, err := s.builds.Create(ctx, model.Build{
		ReviewID:   release.ReviewID,
		Kind:       model.BuildKindRecorded,
		Successful: true,
		CommitID:   release.CommitID,
		Version:    version,
	})
	if err != nil {
		return "", err
	}
	return build.BuildID, nil
}

// releaseFailureReason is what the release row records when the Job does not
// succeed. The commit and the branch are the control plane's own facts;
// everything actionable about *why* comes from the release itself, which already
// printed the coordinates it worked with. Without that detail the record says
// only that a Job exited, which is nothing to act on.
func releaseFailureReason(params releaseexec.ReleaseJobParams, result releaseexec.Result) string {
	reason := fmt.Sprintf("release job %s for %s at commit %s", result.Outcome, params.TargetBranch, params.CommitID)
	if detail := strings.TrimSpace(result.Failure); detail != "" {
		return reason + ": " + detail
	}
	jobName := releaseexec.ReleaseJobName(params.Tenant, params.TargetBranch, params.ReleaseID, params.Attempt)
	return reason + " and left no reason behind (its pod was already reclaimed); `kubectl -n " + params.Namespace +
		" logs job/" + jobName + "` while a release is in flight shows what it is doing"
}

// fail records the failed status best-effort and returns the underlying cause,
// so a write hiccup never masks the real release failure. A lost failure write is
// what would leave the tenant's queue blocked behind a release that is no longer
// running, so it is logged rather than dropped.
func (s *ReleaseService) fail(ctx context.Context, release model.Release, reason string, cause error) error {
	if err := s.releases.RecordOutcome(ctx, release.ReleaseID, repository.ReleaseOutcome{
		Status:        model.ReleaseStatusFailed,
		FailureReason: reason,
	}); err != nil {
		log.Printf("erun api release: recording the failed status for release=%q did not persist: %v (release failure: %v)", release.ReleaseID, err, cause)
	}
	return cause
}
