package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
)

// resolveReportedRepository refuses a remoteURL that contradicts the review's
// recorded repository, so a commit elsewhere cannot verify this review.
func resolveReportedRepository(review model.Review, remoteURL string) (string, error) {
	reported, err := modelpkg.RepositoryIdentity(remoteURL)
	if err != nil {
		return "", &MergeNotVerifiedError{Reason: err.Error()}
	}
	recorded := strings.TrimSpace(review.Repository)
	if recorded != "" && recorded != reported {
		return "", &MergeNotVerifiedError{Reason: fmt.Sprintf(
			"review %s belongs to %s, but the reported remote names %s", review.ReviewID, recorded, reported)}
	}
	return reported, nil
}

// acceptMerged is the queue's path to MERGED: it requires a successful build of
// this review whose commit is verifiably on the target branch past the gated tip.
func (s *ReviewService) acceptMerged(ctx context.Context, review model.Review, buildID, remoteURL, repositoryIdentity string) (model.Review, error) {
	if review.Status != model.ReviewStatusMerge {
		return model.Review{}, &InvalidTransitionError{From: review.Status, To: model.ReviewStatusMerged, ValidTargets: validTargetsFor(review.Status)}
	}
	if buildID == "" {
		return model.Review{}, &MissingBuildIDError{Status: model.ReviewStatusMerged}
	}
	// Captured before the assignment below overwrites it.
	adoptedFromNone := strings.TrimSpace(review.Repository) == ""
	build, err := s.builds.Get(ctx, review.TenantID, buildID)
	if err != nil {
		return model.Review{}, err
	}
	if err := s.verifyMergeBuild(review, build); err != nil {
		return model.Review{}, err
	}
	if err := s.verifyRepositoryState(ctx, review, repositoryIdentity, build.CommitID, remoteURL); err != nil {
		return model.Review{}, err
	}

	review.Status = model.ReviewStatusMerged
	review.LastMergedBuildID = build.BuildID
	// Recorded only once accepted: a legacy review adopts the identity both checks
	// above were answered against.
	review.Repository = repositoryIdentity
	if adoptedFromNone {
		log.Printf("erun api reviews: review %s recorded no repository and adopted %s from its accepted MERGED report", review.ReviewID, repositoryIdentity)
	}
	updated, err := s.reviews.Update(ctx, review)
	if err != nil {
		return model.Review{}, err
	}
	if err := s.reviews.DeleteMergeQueueEntryByReview(ctx, updated.ReviewID); err != nil {
		return model.Review{}, err
	}
	s.triggerRelease(ctx, updated, build.CommitID)
	return updated, nil
}

// reconcileMerged accepts MERGED for work that landed outside the queue (e.g. a
// GitHub squash merge), verified via ContainsChanges. It triggers no release.
func (s *ReviewService) reconcileMerged(ctx context.Context, review model.Review, remoteURL, repositoryIdentity string) (model.Review, error) {
	if review.Status == model.ReviewStatusMerged || review.Status == model.ReviewStatusClosed {
		return model.Review{}, &InvalidTransitionError{From: review.Status, To: model.ReviewStatusMerged, ValidTargets: validTargetsFor(review.Status)}
	}
	if s.verifier == nil {
		return model.Review{}, &MergeNotVerifiedError{Reason: "this control plane has no way to verify merges against the real repository"}
	}
	contained, commit, err := s.verifier.ContainsChanges(ctx, remoteURL, review.TargetBranch, review.SourceBranch)
	if err != nil {
		return model.Review{}, &MergeNotVerifiedError{Reason: err.Error()}
	}
	if !contained {
		return model.Review{}, &MergeNotVerifiedError{Reason: fmt.Sprintf("branch %s adds changes that are not already in %s", review.SourceBranch, review.TargetBranch)}
	}

	review.Status = model.ReviewStatusMerged
	// No build exists, and FindLastMergedReview skips build-less merges so the next
	// gated tip stays anchored on a build's commit.
	review.LastMergedBuildID = ""
	review.Repository = repositoryIdentity
	updated, err := s.reviews.Update(ctx, review)
	if err != nil {
		return model.Review{}, err
	}
	if err := s.reviews.DeleteMergeQueueEntryByReview(ctx, updated.ReviewID); err != nil {
		return model.Review{}, err
	}
	log.Printf("erun api reviews: review %s reconciled MERGED: branch %s landed on %s as %s", updated.ReviewID, updated.SourceBranch, updated.TargetBranch, commit)
	return updated, nil
}

// verifyMergeBuild requires a successful build recorded against this review; the
// build kind is not consulted.
func (s *ReviewService) verifyMergeBuild(review model.Review, build model.Build) error {
	if build.ReviewID != review.ReviewID {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("build %s is not a build of review %s", build.BuildID, review.ReviewID)}
	}
	if !build.Successful {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("build %s did not succeed", build.BuildID)}
	}
	// The READY build is of the source branch, not the merge.
	if build.BuildID == review.LastReadyBuildID {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("build %s is the READY build of source commit %s; record the build of the merge commit that landed this review", build.BuildID, build.CommitID)}
	}
	return nil
}

// verifyRepositoryState requires the reported commit to be on the target branch
// and to strictly descend from the gated tip. Ancestry, not parent equality,
// because release pushes `[skip ci]` commits in between.
//
// Both checks use repositoryIdentity, never the review's column: an empty column
// means "every repository" to FindLastMergedReview and would anchor on a stranger's merge.
func (s *ReviewService) verifyRepositoryState(ctx context.Context, review model.Review, repositoryIdentity, commit, remoteURL string) error {
	if s.verifier == nil {
		return &MergeNotVerifiedError{Reason: "this control plane has no way to verify merges against the real repository"}
	}
	onBranch, _, err := s.verifier.Contains(ctx, remoteURL, review.TargetBranch, commit)
	if err != nil {
		return &MergeNotVerifiedError{Reason: err.Error()}
	}
	if !onBranch {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("commit %s is not on the target branch %s", commit, review.TargetBranch)}
	}
	gatedTip, err := s.gatedTargetTip(ctx, repositoryIdentity, review.TargetBranch)
	if err != nil {
		return err
	}
	if gatedTip == "" {
		return nil
	}
	if strings.EqualFold(gatedTip, commit) {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("commit %s is the target tip this review was gated against; record the build of the merge commit that landed this review", commit)}
	}
	descendsFromGatedTip, err := s.verifier.IsAncestor(ctx, remoteURL, review.TargetBranch, gatedTip, commit)
	if err != nil {
		return &MergeNotVerifiedError{Reason: err.Error()}
	}
	if !descendsFromGatedTip {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("commit %s does not descend from %s, the target tip this review was gated against", commit, gatedTip)}
	}
	return nil
}

// gatedTargetTip is the merge commit of the latest MERGED review on targetBranch
// in repositoryIdentity; the one-MERGE-per-branch invariant makes it the gated
// base. Empty with no error means nothing has merged yet.
func (s *ReviewService) gatedTargetTip(ctx context.Context, repositoryIdentity, targetBranch string) (string, error) {
	last, err := s.reviews.FindLastMergedReview(ctx, repositoryIdentity, targetBranch)
	if errors.Is(err, repository.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	build, err := s.builds.Get(ctx, last.TenantID, last.LastMergedBuildID)
	if err != nil {
		return "", err
	}
	return build.CommitID, nil
}
