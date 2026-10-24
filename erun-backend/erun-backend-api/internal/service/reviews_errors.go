package service

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// UnresolvedThreadsError refuses AdvanceMergeQueue while the queue head has open threads.
type UnresolvedThreadsError struct {
	ReviewID          string
	UnresolvedThreads int
}

func (e *UnresolvedThreadsError) Error() string {
	return fmt.Sprintf("review %s has %d unresolved comment thread(s); resolve them before advancing the merge queue", e.ReviewID, e.UnresolvedThreads)
}

// ErrInvalidTargetBranch is distinct from the blank-reason refusal so a caller can name the field.
var ErrInvalidTargetBranch = fmt.Errorf("targetBranch is required: %w", repository.ErrInvalidInput)

// EmptyMergeQueueError is the EMPTY_QUEUE case, distinct from the bare ErrNotFound refusals.
type EmptyMergeQueueError struct {
	TargetBranch string
}

func (e *EmptyMergeQueueError) Error() string {
	return fmt.Sprintf("no review waiting to merge for target branch %s", e.TargetBranch)
}

func (e *EmptyMergeQueueError) Unwrap() error { return repository.ErrNotFound }

// MergeQueueOccupiedError names the review holding the branch's single MERGE slot.
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

// AmbiguousMergeQueueError refuses to promote an unfiltered queue holding several
// repositories' reviews. Unrecorded reviews are listed separately: they cannot
// be attributed to any named repository.
type AmbiguousMergeQueueError struct {
	TargetBranch        string
	Repositories        []string
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

// ReviewNotMergingError refuses a requeue of a review not holding MERGE.
type ReviewNotMergingError struct {
	ReviewID string
	Status   model.ReviewStatus
}

func (e *ReviewNotMergingError) Error() string {
	return fmt.Sprintf("review %s is %s, not MERGE; only a review holding the merge queue's slot can be requeued back to READY", e.ReviewID, e.Status)
}

func (e *ReviewNotMergingError) Unwrap() error { return repository.ErrConflict }

// InvalidTransitionError refuses a caller asserting MERGE, or MERGED from anything but MERGE.
type InvalidTransitionError struct {
	From         model.ReviewStatus
	To           model.ReviewStatus
	ValidTargets []model.ReviewStatus
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("cannot transition review from %s directly to %s", e.From, e.To)
}

func (e *InvalidTransitionError) Unwrap() error { return repository.ErrInvalidInput }

// MergeNotVerifiedError refuses a MERGED report the platform could not confirm
// against the real repository.
type MergeNotVerifiedError struct {
	Reason string
}

func (e *MergeNotVerifiedError) Error() string { return "merge not verified: " + e.Reason }

func (e *MergeNotVerifiedError) Unwrap() error { return repository.ErrInvalidInput }

// MissingBuildIDError refuses a FAILED/READY update without a buildId.
type MissingBuildIDError struct {
	Status model.ReviewStatus
}

func (e *MissingBuildIDError) Error() string {
	return fmt.Sprintf("buildId is required when setting status to %s", e.Status)
}

func (e *MissingBuildIDError) Unwrap() error { return repository.ErrInvalidInput }

// InvalidRepositoryError refuses a repository that cannot be canonicalized.
type InvalidRepositoryError struct {
	Reason string
}

func (e *InvalidRepositoryError) Error() string { return e.Reason }

func (e *InvalidRepositoryError) Unwrap() error { return repository.ErrInvalidInput }

// InvalidIssueRefError refuses an issue reference that cannot be spelled canonically.
type InvalidIssueRefError struct {
	Reason string
}

func (e *InvalidIssueRefError) Error() string { return e.Reason }

func (e *InvalidIssueRefError) Unwrap() error { return repository.ErrInvalidInput }
