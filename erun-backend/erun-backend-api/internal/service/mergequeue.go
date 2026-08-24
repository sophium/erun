package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mergeexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// MergeReviewRepository is the raw persistence MergeQueueService needs to
// apply the MERGE -> MERGED/FAILED transition itself. It deliberately depends
// on the repository, not on ReviewService: ReviewService must stay free of any
// dependency on the merge executor (see AGENTS.md "Merge Queue"), because the
// executor already depends on ReviewService's own MarkBuildResult by way of
// BuildService, and Go composition cannot wire the resulting cycle.
type MergeReviewRepository interface {
	Get(ctx context.Context, reviewID string) (model.Review, error)
	Update(ctx context.Context, review model.Review) (model.Review, error)
	DeleteMergeQueueEntryByReview(ctx context.Context, reviewID string) error
}

// MergeBuildRepository records the gate build. The raw BuildRepository
// satisfies this directly — recordAndTransition applies the review transition
// itself, so this must not be BuildService (which would call MarkBuildResult a
// second time for the same build).
type MergeBuildRepository interface {
	Create(ctx context.Context, build model.Build) (model.Build, error)
}

// MergeRunner runs one merge-gate attempt to a terminal result — satisfied by
// the mergeexec Job launcher.
type MergeRunner interface {
	Run(ctx context.Context, params mergeexec.MergeJobParams) (mergeexec.Result, error)
}

// ReleaseTrigger enqueues the release a completed merge earns. Shared with the
// interface routes.ReviewRoutes used to hold before MERGED became the merge
// queue's own write, not a caller's PATCH.
type ReleaseTrigger interface {
	TriggerRelease(ctx context.Context, request ReleaseRequest) error
}

// defaultMergeFailureReason is recorded when the Job's own log carried nothing
// usable, so the required failure_detail is never empty.
const defaultMergeFailureReason = "the merge queue job did not report a reason"

// MergeQueueService runs one review's merge-gate attempt: it builds the
// prospective merge of the review's source onto its current target, gates it
// with a real `erun build`, and records what happened. MERGED is written here,
// and only here, once a merge has actually landed and its gate build actually
// passed.
type MergeQueueService struct {
	reviews MergeReviewRepository
	builds  MergeBuildRepository
	runner  MergeRunner
	release ReleaseTrigger
}

func NewMergeQueueService(reviews MergeReviewRepository, builds MergeBuildRepository, runner MergeRunner, release ReleaseTrigger) *MergeQueueService {
	return &MergeQueueService{reviews: reviews, builds: builds, runner: runner, release: release}
}

// Run executes one review's merge-gate attempt. The review is already `MERGE`
// — AdvanceMergeQueue put it there — so every path out of here has to leave a
// gate build recorded and the review moved to a terminal status, or the review
// (and the target branch's queue behind it) stays stuck on an attempt nothing
// ever answered.
func (s *MergeQueueService) Run(ctx context.Context, reviewID, targetBranch string, params mergeexec.MergeJobParams) error {
	if s.runner == nil {
		return s.recordAndTransition(ctx, reviewID, "", false,
			"this control plane has no merge executor configured, so the review was promoted but its gate cannot run here")
	}
	result, err := s.runner.Run(ctx, params)
	if err != nil {
		return s.recordAndTransition(ctx, reviewID, "", false, err.Error())
	}
	if result.Outcome != mergeexec.OutcomeSucceeded {
		commit := firstNonEmpty(result.MergeCommit, result.SourceCommit)
		return s.recordAndTransition(ctx, reviewID, commit, false, mergeFailureReason(params, result))
	}
	if result.MergeCommit == "" {
		// A green Job that never reported the commit it merged and pushed is not
		// a success this control plane can record: nothing can name what landed.
		return s.recordAndTransition(ctx, reviewID, result.SourceCommit, false,
			"the merge job exited successfully but never reported the commit it merged and pushed, so nothing can name what landed")
	}
	if err := s.recordAndTransition(ctx, reviewID, result.MergeCommit, true, ""); err != nil {
		return err
	}
	return s.triggerRelease(ctx, reviewID, targetBranch, result.MergeCommit)
}

// recordAndTransition records the gate build and applies the resulting review
// transition in one place, so the two can never disagree about what happened.
// A review that has already moved off MERGE (an operator's manual
// intervention) is left alone: there is nothing left for this attempt to
// record against.
func (s *MergeQueueService) recordAndTransition(ctx context.Context, reviewID, commit string, successful bool, failureReason string) error {
	review, err := s.reviews.Get(ctx, reviewID)
	if err != nil {
		return err
	}
	if review.Status != model.ReviewStatusMerge {
		return nil
	}

	build := model.Build{ReviewID: reviewID, Successful: successful, CommitID: commitOrPlaceholder(commit), Kind: model.BuildKindGate}
	if !successful {
		build.FailureDetail = firstNonEmpty(strings.TrimSpace(failureReason), defaultMergeFailureReason)
	}
	created, err := s.builds.Create(ctx, build)
	if err != nil {
		return err
	}

	if successful {
		review.Status = model.ReviewStatusMerged
		review.LastMergedBuildID = created.BuildID
		_, err := s.reviews.Update(ctx, review)
		return err
	}
	review.Status = model.ReviewStatusFailed
	review.LastFailedBuildID = created.BuildID
	if _, err := s.reviews.Update(ctx, review); err != nil {
		return err
	}
	return s.reviews.DeleteMergeQueueEntryByReview(ctx, reviewID)
}

func (s *MergeQueueService) triggerRelease(ctx context.Context, reviewID, targetBranch, commit string) error {
	if s.release == nil {
		return nil
	}
	return s.release.TriggerRelease(ctx, ReleaseRequest{ReviewID: reviewID, TargetBranch: targetBranch, CommitID: commit})
}

// mergeFailureReason is what the gate build's failure_detail carries: the
// control plane's own coordinates plus, whenever the Job left anything behind,
// the gate's own words for why it did not succeed.
func mergeFailureReason(params mergeexec.MergeJobParams, result mergeexec.Result) string {
	reason := fmt.Sprintf("merge gate %s for %s <- %s", result.Outcome, params.TargetBranch, params.SourceBranch)
	if detail := strings.TrimSpace(result.Failure); detail != "" {
		return reason + ": " + detail
	}
	return reason + " and left no reason behind"
}

// commitOrPlaceholder guarantees builds.commit_id's non-empty check is
// satisfiable even when the Job produced no output at all (never scheduled, or
// its pod was reclaimed before anything could be read back).
func commitOrPlaceholder(commit string) string {
	if strings.TrimSpace(commit) != "" {
		return commit
	}
	return "(unknown: the merge job produced no output)"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
