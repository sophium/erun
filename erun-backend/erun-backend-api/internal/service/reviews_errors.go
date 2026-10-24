package service

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// UnresolvedThreadsError refuses AdvanceMergeQueue when the queue head still
// has open comment threads. It carries what a caller needs both to report the
// block and to route the operator to the threads, rather than a bare error
// string a caller would have to parse.
type UnresolvedThreadsError struct {
	ReviewID          string
	UnresolvedThreads int
}

func (e *UnresolvedThreadsError) Error() string {
	return fmt.Sprintf("review %s has %d unresolved comment thread(s); resolve them before advancing the merge queue", e.ReviewID, e.UnresolvedThreads)
}

// ErrInvalidTargetBranch refuses an empty targetBranch on merge-queue
// advance — distinguished from OverrideAdvanceMergeQueue's blank-reason
// refusal (also ErrInvalidInput) so a caller can be told which field it was.
var ErrInvalidTargetBranch = fmt.Errorf("targetBranch is required: %w", repository.ErrInvalidInput)

// EmptyMergeQueueError distinguishes "nothing waiting to promote" from the
// review-already-merging case headOfMergeQueue also refuses with the bare
// ErrNotFound sentinel — both keep the same 404, but only this one is the
// EMPTY_QUEUE machine code documented in collaboration/reviews.md.
type EmptyMergeQueueError struct {
	TargetBranch string
}

func (e *EmptyMergeQueueError) Error() string {
	return fmt.Sprintf("no review waiting to merge for target branch %s", e.TargetBranch)
}

func (e *EmptyMergeQueueError) Unwrap() error { return repository.ErrNotFound }

// MergeQueueOccupiedError refuses an advance while another review on the same
// target branch already holds the queue's single MERGE slot. It carries that
// review because the refusal's whole job is to name the blocker: reported as a
// bare not-found it reads as a missing resource — a typo'd endpoint, a deleted
// review — and sends the operator looking for something that was never wrong,
// when the review to finish or requeue is the one thing they need.
type MergeQueueOccupiedError struct {
	TargetBranch string
	ReviewID     string
	Name         string
	SourceBranch string
}

func (e *MergeQueueOccupiedError) Error() string {
	return fmt.Sprintf("merge queue for %s already has a review at MERGE: %s (%s, %s); complete it or requeue it back to READY before advancing",
		e.TargetBranch, e.ReviewID, e.Name, e.SourceBranch)
}

func (e *MergeQueueOccupiedError) Unwrap() error { return repository.ErrConflict }

// AmbiguousMergeQueueError refuses to promote from a queue the caller did not
// settle on a repository. A target branch alone names one queue only in a
// tenant that serves exactly one repository; where several have reviews
// waiting, promoting "the head" would gate whichever repository's review
// happened to sort first — a branch that need not exist in the checkout the
// gate runs in. It names the repositories so the caller can name one.
//
// It names the waiting reviews that record no repository too, and says what
// they are. Those rows do not make the queue ambiguous — an unrecorded
// repository is the absence of an answer, not a second one — but in a queue
// that is genuinely several repositories' they can be attributed to none of
// them, so a caller told only the repositories would be left to work out for
// itself which rows are stuck.
type AmbiguousMergeQueueError struct {
	TargetBranch string
	Repositories []string
	// UnrecordedReviewIDs are the queued reviews that record no repository.
	UnrecordedReviewIDs []string
}

func (e *AmbiguousMergeQueueError) Error() string {
	message := fmt.Sprintf("the merge queue for %s holds reviews from more than one repository (%s); name the one to advance",
		e.TargetBranch, strings.Join(e.Repositories, ", "))
	if len(e.UnrecordedReviewIDs) > 0 {
		message += fmt.Sprintf("; %d review(s) in that queue record no repository and can be attributed to none of them (%s)",
			len(e.UnrecordedReviewIDs), strings.Join(e.UnrecordedReviewIDs, ", "))
	}
	return message
}

func (e *AmbiguousMergeQueueError) Unwrap() error { return repository.ErrConflict }

// ReviewNotMergingError refuses the missed-merge-window requeue on a review
// that is not holding MERGE. The review was already resolved by id, so it
// exists and the caller can see it: reporting a not-found there describes a
// missing resource for a review sitting in plain sight, and leaves "requeue
// did not work" with nothing to act on. The status is what actually explains
// the refusal.
type ReviewNotMergingError struct {
	ReviewID string
	Status   model.ReviewStatus
}

func (e *ReviewNotMergingError) Error() string {
	return fmt.Sprintf("review %s is %s, not MERGE; only a review holding the merge queue's slot can be requeued back to READY", e.ReviewID, e.Status)
}

func (e *ReviewNotMergingError) Unwrap() error { return repository.ErrConflict }

// InvalidTransitionError refuses a caller's PATCH .../status asserting MERGE
// directly, or MERGED from any status other than MERGE — AdvanceMergeQueue is
// the only path to MERGE, and MERGED from MERGE still has to pass
// verifyMergeBuild/verifyRepositoryState below.
type InvalidTransitionError struct {
	From         model.ReviewStatus
	To           model.ReviewStatus
	ValidTargets []model.ReviewStatus
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("cannot transition review from %s directly to %s", e.From, e.To)
}

func (e *InvalidTransitionError) Unwrap() error { return repository.ErrInvalidInput }

// MergeNotVerifiedError refuses a MERGED report the platform could not
// independently confirm against the real repository: a build that is not a
// successful build of this review, or a build whose commit is not verifiably
// on the target branch, descended from — and not equal to — the tip this
// review was gated against. Whoever calls UpdateStatus, the transition only happens when this
// check passes — see AGENTS.md "Merge Queue".
type MergeNotVerifiedError struct {
	Reason string
}

func (e *MergeNotVerifiedError) Error() string { return "merge not verified: " + e.Reason }

func (e *MergeNotVerifiedError) Unwrap() error { return repository.ErrInvalidInput }

// MissingBuildIDError refuses a FAILED/READY status update with no buildId:
// both statuses record which build produced them.
type MissingBuildIDError struct {
	Status model.ReviewStatus
}

func (e *MissingBuildIDError) Error() string {
	return fmt.Sprintf("buildId is required when setting status to %s", e.Status)
}

func (e *MissingBuildIDError) Unwrap() error { return repository.ErrInvalidInput }

// InvalidRepositoryError refuses a repository the platform cannot canonicalize
// — an empty host, a bare forge with no repository path — rather than storing
// a value no other caller could ever match.
type InvalidRepositoryError struct {
	Reason string
}

func (e *InvalidRepositoryError) Error() string { return e.Reason }

func (e *InvalidRepositoryError) Unwrap() error { return repository.ErrInvalidInput }

// InvalidIssueRefError refuses an issue reference the platform cannot spell
// canonically — prose, a branch slug, a bare number the recorded repository
// cannot be joined to — rather than storing a reference no other pipeline
// could ever match it against.
type InvalidIssueRefError struct {
	Reason string
}

func (e *InvalidIssueRefError) Error() string { return e.Reason }

func (e *InvalidIssueRefError) Unwrap() error { return repository.ErrInvalidInput }
