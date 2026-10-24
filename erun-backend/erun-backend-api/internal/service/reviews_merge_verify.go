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

// resolveReportedRepository answers which repository the report's
// remoteURL names, refusing a remote that contradicts the repository the
// review already records. Verification is only meaningful against the
// review's own repository: a caller pointing the platform at a different one
// could have it confirm a commit that landed somewhere else entirely, which
// is the confusion a review without repository identity made possible.
//
// The identity is canonicalized first, so the SSH remote a checkout reports
// verifies against the HTTPS one the review recorded.
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

// acceptMerged is the one path to MERGED, open to any caller — the guarantee
// is no longer who calls it, but what it can verify: a successful build
// already recorded against this exact review (verifyMergeBuild), and that
// build's commit's real presence on the target branch, descended from and
// distinct from the tip this review was gated against
// (verifyRepositoryState). Any check
// failing refuses with *MergeNotVerifiedError; nothing about the review
// changes.
func (s *ReviewService) acceptMerged(ctx context.Context, review model.Review, buildID, remoteURL, repositoryIdentity string) (model.Review, error) {
	if review.Status != model.ReviewStatusMerge {
		return model.Review{}, &InvalidTransitionError{From: review.Status, To: model.ReviewStatusMerged, ValidTargets: validTargetsFor(review.Status)}
	}
	if buildID == "" {
		return model.Review{}, &MissingBuildIDError{Status: model.ReviewStatusMerged}
	}
	// Whether this row is adopting an identity it never had, captured before
	// the assignment below overwrites the evidence.
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
	// Recorded only now, once the merge is accepted: a review created before
	// the platform recorded a repository adopts the one this report named,
	// which is the only moment the platform ever holds it. A refused report
	// changes nothing. The same identity is what both verification conditions
	// above were answered against, so what the row ends up recording is what
	// the check actually established, and not a second, unstated choice.
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

// reconcileMerged is the other way to MERGED, for a review whose work landed
// through something other than this platform's merge queue — in practice a
// GitHub squash merge, where the branch's own commits are deliberately not
// made ancestors of the target and no merge build was ever recorded, so
// neither of acceptMerged's conditions can ever hold no matter how long the
// review sits there. Without this the only way out was CLOSED, which renders
// landed work as abandoned and so is worse than leaving it OPEN — and the
// OPEN count grows by one for every change that lands this way.
//
// The platform still verifies rather than believes: ContainsChanges confirms
// against the real remote that everything the source branch adds is present
// in the target branch's history. What is reported here already happened
// elsewhere, so unlike acceptMerged this triggers no release.
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
	// Deliberately no LastMergedBuildID: there was no build, and
	// FindLastMergedReview skips build-less merges for exactly this reason —
	// gatedTargetTip anchors the next queue-driven merge on a build's commit,
	// which a reconciliation has none of. The repository this report named is
	// recorded, though: that is review identity, not build provenance.
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

// verifyMergeBuild is condition 3: a successful build already recorded
// against this exact review, not a caller's word for it. The build's kind is
// not consulted: the merge commit's build is an ordinary recorded build, and a
// historical GATE build of a review already at MERGE still qualifies. Which
// commit the build is of is verifyRepositoryState's question.
func (s *ReviewService) verifyMergeBuild(review model.Review, build model.Build) error {
	if build.ReviewID != review.ReviewID {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("build %s is not a build of review %s", build.BuildID, review.ReviewID)}
	}
	if !build.Successful {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("build %s did not succeed", build.BuildID)}
	}
	// The READY build is the source branch's, not the merge's: even where its
	// commit reached the target unchanged, the merge was never built.
	if build.BuildID == review.LastReadyBuildID {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("build %s is the READY build of source commit %s; record the build of the merge commit that landed this review", build.BuildID, build.CommitID)}
	}
	return nil
}

// verifyRepositoryState is conditions 1 and 2: the reported commit is
// verifiably on the target branch, and the target tip this review was gated
// against — the platform's own record of what that tip was, since only one
// review may be MERGE per target branch at a time (see AGENTS.md "Merge
// Queue") — is still really its ancestor, and is not the commit itself. A
// commit is its own ancestor, so a build of the gated tip would otherwise
// pass while naming a merge in which nothing landed. This is a reachability check, not
// a strict parent-equality one: the release flow pushes its own
// `[skip ci]` commits directly to the target branch between one review
// landing and the next being reported, and requiring the
// reported commit's immediate parent to equal the gated tip made every
// review report unverifiable forever after the first release. Ancestry
// tolerates any number of unrelated commits landing in between while still
// refusing a commit whose history never really passed through the gated
// tip at all — the case that matters, a rewritten or replaced history
// (a force-push standing in for a buggy or malicious reporter).
//
// Both conditions are asked of repositoryIdentity — the repository the
// report's remote names — and never of the review's own repository column,
// which is empty on a row created before the platform recorded one. An empty
// repository means "every repository" to FindLastMergedReview's filter, so
// anchoring condition 2 on the column rather than on the identity would let
// whichever repository merged onto a same-named target branch most recently
// stand in as this row's gated base: it refuses a real landing because a
// stranger's commit is not one of its ancestors, and it defeats the force-push
// check for a repository whose own history really was rewritten, by finding
// some other repository's commit where it should have found that repository's.
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

// gatedTargetTip is the merge commit of the most recently MERGED review on
// targetBranch in repositoryIdentity — still the right anchor even though it
// can no longer be compared by strict equality: the one-MERGE-per-target-branch
// invariant means nothing else advances the queue's own notion of the branch's
// tip while a review holds MERGE, so this is the review's actual gated base
// regardless of what else (a release push) landed on the branch around it.
//
// The repository is a parameter rather than something read off a review
// because the two are not always the same question, and passing the wrong one
// here is silent: a review that recorded no repository has to be anchored on
// the repository its report names — the one it adopts — since asking for
// "any repository" would answer with a stranger's merge. Empty with no error
// means this repository has never merged onto this branch through the queue
// yet — the bootstrap case, with nothing recorded to compare against.
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
