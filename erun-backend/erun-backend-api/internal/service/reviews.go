package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type ReviewRepository interface {
	Get(ctx context.Context, reviewID string) (model.Review, error)
	Update(ctx context.Context, review model.Review) (model.Review, error)
	FindNextMergeQueueReview(ctx context.Context, targetBranch string) (model.Review, error)
	FindActiveMergeReview(ctx context.Context, targetBranch string) (model.Review, error)
	// FindLastMergedReview is the platform's own record of what targetBranch's
	// tip was the last time a queue-driven merge landed on it — condition 2 of
	// accepting a MERGED report compares a reported commit's parent against
	// it. repository.ErrNotFound means no review has ever merged onto this
	// branch through the queue yet.
	FindLastMergedReview(ctx context.Context, targetBranch string) (model.Review, error)
	CreateMergeQueueEntry(ctx context.Context, entry model.ReviewMergeQueueEntry) (model.ReviewMergeQueueEntry, error)
	DeleteMergeQueueEntryByReview(ctx context.Context, reviewID string) error
}

type ReviewBuildRepository interface {
	Get(ctx context.Context, buildID string) (model.Build, error)
}

// MergeVerifier confirms a reported merge commit is really on the target
// branch's real remote, and reports its actual parent — the fact-about-the-
// repository check that replaces trusting whoever calls UpdateStatus with
// MERGED. See AGENTS.md "Merge Queue".
type MergeVerifier interface {
	Contains(ctx context.Context, remoteURL, branch, commit string) (ok bool, parent string, err error)
}

// ReleaseTrigger enqueues the release a completed merge earns.
type ReleaseTrigger interface {
	TriggerRelease(ctx context.Context, request ReleaseRequest) error
}

// ReviewCommentRepository is the narrow read access AdvanceMergeQueue needs to
// gate on a review's unresolved comment threads.
type ReviewCommentRepository interface {
	List(ctx context.Context, filter repository.CommentFilter) ([]model.Comment, error)
}

// ReviewAuditLogger records OverrideAdvanceMergeQueue's bypass. It is the raw
// audit repository, not a transport-facing type, so the service stays free of
// any HTTP dependency beyond the request-scoped security context it already
// reads (see CommentService.PrepareCreate for the same pattern).
type ReviewAuditLogger interface {
	LogAuditEvent(ctx context.Context, event model.AuditEvent) error
}

// overrideAdvanceMergeQueueAPIPath is the canonical route template
// routes.RegisterReviewRoutes registers OverrideAdvanceMergeQueue's HTTP
// entrypoint at. It must match that registration exactly: it is what the
// override's audit event records as api_path, and what the API's own
// permission-by-path authorization keys the override on.
const overrideAdvanceMergeQueueAPIPath = "/v1/reviews/merge-queue/override-advance"

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

// InvalidTransitionError refuses a caller's PATCH .../status asserting MERGE
// directly, or MERGED from any status other than MERGE — AdvanceMergeQueue is
// the only path to MERGE, and MERGED from MERGE still has to pass
// verifyGateBuild/verifyRepositoryState below.
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
// independently confirm against the real repository: a successful GATE build
// recorded for a different commit or review, or a reported commit that is
// not verifiably on the target branch with the parent this review was gated
// against. Whoever calls UpdateStatus, the transition only happens when this
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

// validTargetsFor lists the statuses a caller's PATCH .../status may set from
// the review's current status, mirroring the Status lifecycle documented in
// collaboration/reviews.md. MERGE never appears: only AdvanceMergeQueue
// reaches it. MERGED appears only from MERGE, and even then only once
// verifyGateBuild/verifyRepositoryState pass.
func validTargetsFor(status model.ReviewStatus) []model.ReviewStatus {
	switch status {
	case model.ReviewStatusOpen:
		return []model.ReviewStatus{model.ReviewStatusFailed, model.ReviewStatusReady, model.ReviewStatusClosed}
	case model.ReviewStatusFailed:
		return []model.ReviewStatus{model.ReviewStatusReady, model.ReviewStatusClosed}
	case model.ReviewStatusReady:
		return []model.ReviewStatus{model.ReviewStatusClosed}
	case model.ReviewStatusMerge:
		return []model.ReviewStatus{model.ReviewStatusReady, model.ReviewStatusMerged}
	default:
		return nil
	}
}

type ReviewService struct {
	reviews  ReviewRepository
	builds   ReviewBuildRepository
	comments ReviewCommentRepository
	audit    ReviewAuditLogger
	// verifier and release are both optional: nil verifier refuses every
	// MERGED report (see verifyRepositoryState), and nil release simply
	// leaves an accepted merge's release un-triggered rather than erroring.
	verifier MergeVerifier
	release  ReleaseTrigger
}

func NewReviewService(reviews ReviewRepository, builds ReviewBuildRepository, comments ReviewCommentRepository, audit ReviewAuditLogger, verifier MergeVerifier, release ReleaseTrigger) *ReviewService {
	return &ReviewService{reviews: reviews, builds: builds, comments: comments, audit: audit, verifier: verifier, release: release}
}

func (s *ReviewService) PrepareCreate(review model.Review) model.Review {
	if review.Status == "" {
		review.Status = model.ReviewStatusOpen
	}
	return review
}

// AdvanceMergeQueue promotes targetBranch's queue head to MERGE, refusing
// (UnresolvedThreadsError) when that review still has open comment threads.
// The check is authoritative here, not only in a client: a caller that skips
// straight to this endpoint (the CLI, the API directly, a client bug) must
// meet the same bar a careful desktop user would. OverrideAdvanceMergeQueue is
// the one deliberate, audited way past it.
func (s *ReviewService) AdvanceMergeQueue(ctx context.Context, targetBranch string) (model.Review, error) {
	review, err := s.headOfMergeQueue(ctx, targetBranch)
	if err != nil {
		return model.Review{}, err
	}
	unresolved, err := s.unresolvedThreadCount(ctx, review.ReviewID)
	if err != nil {
		return model.Review{}, err
	}
	if unresolved > 0 {
		return model.Review{}, &UnresolvedThreadsError{ReviewID: review.ReviewID, UnresolvedThreads: unresolved}
	}
	return s.promoteToMerge(ctx, review)
}

// OverrideAdvanceMergeQueue bypasses the unresolved-thread gate
// AdvanceMergeQueue enforces. It is the one legitimate escape from that gate,
// so it demands the two things that make a bypass accountable rather than a
// quiet workaround: a caller-stated reason, and a durable audit record of it.
// Both are required — a missing reason or an unconfigured audit logger fails
// closed rather than silently promoting anyway.
func (s *ReviewService) OverrideAdvanceMergeQueue(ctx context.Context, targetBranch, reason string) (model.Review, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return model.Review{}, repository.ErrInvalidInput
	}
	if s.audit == nil {
		return model.Review{}, errors.New("merge queue override requires audit logging, which is not configured on this control plane")
	}
	review, err := s.headOfMergeQueue(ctx, targetBranch)
	if err != nil {
		return model.Review{}, err
	}
	if err := s.auditOverrideAdvance(ctx, review, reason); err != nil {
		return model.Review{}, err
	}
	return s.promoteToMerge(ctx, review)
}

// headOfMergeQueue resolves targetBranch's next queued review, refusing when
// another review is already merging on that branch.
func (s *ReviewService) headOfMergeQueue(ctx context.Context, targetBranch string) (model.Review, error) {
	if targetBranch == "" {
		return model.Review{}, ErrInvalidTargetBranch
	}
	if _, err := s.reviews.FindActiveMergeReview(ctx, targetBranch); err == nil {
		return model.Review{}, repository.ErrNotFound
	} else if !errors.Is(err, repository.ErrNotFound) {
		return model.Review{}, err
	}
	review, err := s.reviews.FindNextMergeQueueReview(ctx, targetBranch)
	if errors.Is(err, repository.ErrNotFound) {
		return model.Review{}, &EmptyMergeQueueError{TargetBranch: targetBranch}
	}
	return review, err
}

// promoteToMerge moves review from the queue to MERGE. Both AdvanceMergeQueue
// and OverrideAdvanceMergeQueue reach here only once their own precondition
// (the thread gate, or the reason+audit bypass) has already been satisfied.
func (s *ReviewService) promoteToMerge(ctx context.Context, review model.Review) (model.Review, error) {
	if err := s.reviews.DeleteMergeQueueEntryByReview(ctx, review.ReviewID); err != nil {
		return model.Review{}, err
	}
	review.Status = model.ReviewStatusMerge
	return s.reviews.Update(ctx, review)
}

// unresolvedThreadCount mirrors eruncommon.CountUnresolvedThreads's rule (a
// root comment, ParentCommentID unset, whose Status is still OPEN) over the
// backend's own model.Comment rather than importing the transport-facing
// erun-common client type for a five-line loop.
func (s *ReviewService) unresolvedThreadCount(ctx context.Context, reviewID string) (int, error) {
	comments, err := s.comments.List(ctx, repository.CommentFilter{ReviewID: reviewID})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, comment := range comments {
		if strings.TrimSpace(comment.ParentCommentID) == "" && comment.Status == model.CommentStatusOpen {
			count++
		}
	}
	return count, nil
}

// auditOverrideParameters is the api_parameters payload shape for a
// merge-queue override, so the field is machine-readable in the audit trail
// rather than a hand-built string.
type auditOverrideParameters struct {
	ReviewID     string `json:"reviewId"`
	TargetBranch string `json:"targetBranch"`
	Reason       string `json:"reason"`
}

func (s *ReviewService) auditOverrideAdvance(ctx context.Context, review model.Review, reason string) error {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return repository.ErrMissingSecurityContext
	}
	parameters, err := json.Marshal(auditOverrideParameters{ReviewID: review.ReviewID, TargetBranch: review.TargetBranch, Reason: reason})
	if err != nil {
		return err
	}
	return s.audit.LogAuditEvent(ctx, model.AuditEvent{
		TenantID:         securityContext.TenantID,
		ErunUserID:       securityContext.ErunUserID,
		ExternalUserID:   securityContext.ExternalUserID,
		ExternalIssuerID: securityContext.ExternalIssuer,
		ExternalOrgID:    securityContext.ExternalOrgID,
		Type:             model.AuditEventTypeAPI,
		APIMethod:        http.MethodPost,
		APIPath:          overrideAdvanceMergeQueueAPIPath,
		APIParameters:    string(parameters),
	})
}

// UpdateStatus applies a caller-reported status transition. remoteURL is
// used only for a MERGED report: it is the target the caller pushed to,
// which acceptMerged fetches to check the reported commit against the real
// repository. Every other transition ignores it.
func (s *ReviewService) UpdateStatus(ctx context.Context, reviewID string, status model.ReviewStatus, buildID string, remoteURL string) (model.Review, error) {
	review, err := s.reviews.Get(ctx, reviewID)
	if err != nil {
		return model.Review{}, err
	}

	// MERGE is reached only by AdvanceMergeQueue promoting the queue head — a
	// caller's PATCH asserting it is an assertion nothing verified.
	if status == model.ReviewStatusMerge {
		return model.Review{}, &InvalidTransitionError{From: review.Status, To: status, ValidTargets: validTargetsFor(review.Status)}
	}

	if status == model.ReviewStatusMerged {
		return s.acceptMerged(ctx, review, buildID, remoteURL)
	}

	// READY without a build is the missed-merge-window path, not a build result.
	if status == model.ReviewStatusReady && buildID == "" {
		return s.requeueMergingReview(ctx, review)
	}

	if reviewLastBuildColumn(status) != "" {
		if buildID == "" {
			return model.Review{}, &MissingBuildIDError{Status: status}
		}
		return s.updateBuildStatus(ctx, review, status, buildID)
	}

	return s.dequeueWithStatus(ctx, review, status)
}

// acceptMerged is the one path to MERGED, open to any caller — the guarantee
// is no longer who calls it, but what it can verify: a successful GATE build
// already recorded against this exact review and commit (verifyGateBuild),
// and that commit's real presence on the target branch with the parent this
// review was gated against (verifyRepositoryState). Any check failing
// refuses with *MergeNotVerifiedError; nothing about the review changes.
func (s *ReviewService) acceptMerged(ctx context.Context, review model.Review, buildID, remoteURL string) (model.Review, error) {
	if review.Status != model.ReviewStatusMerge {
		return model.Review{}, &InvalidTransitionError{From: review.Status, To: model.ReviewStatusMerged, ValidTargets: validTargetsFor(review.Status)}
	}
	if buildID == "" {
		return model.Review{}, &MissingBuildIDError{Status: model.ReviewStatusMerged}
	}
	build, err := s.builds.Get(ctx, buildID)
	if err != nil {
		return model.Review{}, err
	}
	if err := s.verifyGateBuild(review, build); err != nil {
		return model.Review{}, err
	}
	if err := s.verifyRepositoryState(ctx, review, build.CommitID, remoteURL); err != nil {
		return model.Review{}, err
	}

	review.Status = model.ReviewStatusMerged
	review.LastMergedBuildID = build.BuildID
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

// verifyGateBuild is condition 3: a successful GATE build already recorded
// against this exact review, not a caller's word for it.
func (s *ReviewService) verifyGateBuild(review model.Review, build model.Build) error {
	if build.ReviewID != review.ReviewID {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("build %s is not a build of review %s", build.BuildID, review.ReviewID)}
	}
	if build.Kind != model.BuildKindGate {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("build %s is a %s build, not a GATE build", build.BuildID, build.Kind)}
	}
	if !build.Successful {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("build %s did not succeed", build.BuildID)}
	}
	return nil
}

// verifyRepositoryState is conditions 1 and 2: the reported commit is
// verifiably on the target branch, and its parent is the target tip this
// review was gated against — the platform's own record of what that tip was,
// since only one review may be MERGE per target branch at a time (see
// AGENTS.md "Merge Queue"), so nothing else could have moved it in the
// meantime.
func (s *ReviewService) verifyRepositoryState(ctx context.Context, review model.Review, commit, remoteURL string) error {
	if s.verifier == nil {
		return &MergeNotVerifiedError{Reason: "this control plane has no way to verify merges against the real repository"}
	}
	onBranch, parent, err := s.verifier.Contains(ctx, remoteURL, review.TargetBranch, commit)
	if err != nil {
		return &MergeNotVerifiedError{Reason: err.Error()}
	}
	if !onBranch {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("commit %s is not on the target branch %s", commit, review.TargetBranch)}
	}
	expectedParent, err := s.gatedTargetTip(ctx, review.TargetBranch)
	if err != nil {
		return err
	}
	if expectedParent != "" && parent != expectedParent {
		return &MergeNotVerifiedError{Reason: fmt.Sprintf("commit %s's parent %s does not match the target tip %s this review was gated against", commit, parent, expectedParent)}
	}
	return nil
}

// gatedTargetTip is the merge commit of the most recently MERGED review on
// targetBranch. Empty with no error means no review has ever merged onto
// this branch through the queue yet — the bootstrap case, with nothing
// recorded to compare against.
func (s *ReviewService) gatedTargetTip(ctx context.Context, targetBranch string) (string, error) {
	last, err := s.reviews.FindLastMergedReview(ctx, targetBranch)
	if errors.Is(err, repository.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	build, err := s.builds.Get(ctx, last.LastMergedBuildID)
	if err != nil {
		return "", err
	}
	return build.CommitID, nil
}

// triggerRelease enqueues the release a newly MERGED review earns. A
// dispatch failure is not this transition's failure — the review is already
// MERGED — so it is logged rather than returned; an operator can still
// trigger the release for the commit directly.
func (s *ReviewService) triggerRelease(ctx context.Context, review model.Review, commit string) {
	if s.release == nil {
		return
	}
	if err := s.release.TriggerRelease(ctx, ReleaseRequest{ReviewID: review.ReviewID, TargetBranch: review.TargetBranch, CommitID: commit}); err != nil {
		log.Printf("erun api reviews: triggering the release for review %s did not start: %v", review.ReviewID, err)
	}
}

// requeueMergingReview returns a review that missed its merge window to READY at
// the end of its target branch queue; only a merging review can take that path.
func (s *ReviewService) requeueMergingReview(ctx context.Context, review model.Review) (model.Review, error) {
	if review.Status != model.ReviewStatusMerge {
		return model.Review{}, repository.ErrNotFound
	}
	review.Status = model.ReviewStatusReady
	updated, err := s.reviews.Update(ctx, review)
	if err != nil {
		return model.Review{}, err
	}
	if err := s.enqueueReview(ctx, updated); err != nil {
		return model.Review{}, err
	}
	return updated, nil
}

// dequeueWithStatus applies a status no queued review may hold, so the review
// also leaves the merge queue.
func (s *ReviewService) dequeueWithStatus(ctx context.Context, review model.Review, status model.ReviewStatus) (model.Review, error) {
	review.Status = status
	updated, err := s.reviews.Update(ctx, review)
	if err != nil {
		return model.Review{}, err
	}
	if err := s.reviews.DeleteMergeQueueEntryByReview(ctx, updated.ReviewID); err != nil {
		return model.Review{}, err
	}
	return updated, nil
}

// MarkBuildResult applies the review-status transition a recorded build
// triggers. It reports the review a promotion to MERGE landed on (ok=true) so
// the caller can hand it to the merge queue dispatcher — the promoted review is
// not necessarily the one this build belongs to, since a successful build only
// unblocks its own target branch's queue and AdvanceMergeQueue promotes
// whichever review is at the head of it.
func (s *ReviewService) MarkBuildResult(ctx context.Context, reviewID string, buildID string, successful bool) (model.Review, bool, error) {
	review, err := s.reviews.Get(ctx, reviewID)
	if err != nil {
		return model.Review{}, false, err
	}

	if successful {
		return s.markBuildSucceeded(ctx, review, buildID)
	}
	return s.markBuildFailed(ctx, review, buildID)
}

// markBuildSucceeded queues a review whose build passed and lets its target
// branch start merging. A review in any other status has already moved past this
// build, so its status stands.
func (s *ReviewService) markBuildSucceeded(ctx context.Context, review model.Review, buildID string) (model.Review, bool, error) {
	if review.Status != model.ReviewStatusOpen && review.Status != model.ReviewStatusFailed {
		return model.Review{}, false, nil
	}
	review.Status = model.ReviewStatusReady
	review.LastReadyBuildID = buildID
	updated, err := s.reviews.Update(ctx, review)
	if err != nil {
		return model.Review{}, false, err
	}
	if err := s.enqueueReview(ctx, updated); err != nil {
		return model.Review{}, false, err
	}
	// Another review already merging on that branch is the normal case, not a
	// failure of this build. Nor is the queue head having unresolved comment
	// threads: that review is not necessarily the one this build belongs to, so
	// its own gate blocking has nothing to do with whether reporting this build
	// succeeded.
	promoted, err := s.AdvanceMergeQueue(ctx, updated.TargetBranch)
	if err != nil {
		var blocked *UnresolvedThreadsError
		if errors.Is(err, repository.ErrNotFound) || errors.As(err, &blocked) {
			return model.Review{}, false, nil
		}
		return model.Review{}, false, err
	}
	return promoted, true, nil
}

// markBuildFailed fails a review whose build failed and drops it from the merge
// queue. A review past those statuses keeps the status it has.
func (s *ReviewService) markBuildFailed(ctx context.Context, review model.Review, buildID string) (model.Review, bool, error) {
	if review.Status != model.ReviewStatusOpen &&
		review.Status != model.ReviewStatusFailed &&
		review.Status != model.ReviewStatusReady &&
		review.Status != model.ReviewStatusMerge {
		return model.Review{}, false, nil
	}
	review.Status = model.ReviewStatusFailed
	review.LastFailedBuildID = buildID
	if _, err := s.reviews.Update(ctx, review); err != nil {
		return model.Review{}, false, err
	}
	return model.Review{}, false, s.reviews.DeleteMergeQueueEntryByReview(ctx, review.ReviewID)
}

func (s *ReviewService) updateBuildStatus(ctx context.Context, review model.Review, status model.ReviewStatus, buildID string) (model.Review, error) {
	column := reviewLastBuildColumn(status)
	if column == "" {
		return model.Review{}, repository.ErrInvalidInput
	}
	build, err := s.builds.Get(ctx, buildID)
	if err != nil {
		return model.Review{}, err
	}
	if build.ReviewID != review.ReviewID || build.Successful != (status != model.ReviewStatusFailed) {
		return model.Review{}, repository.ErrNotFound
	}

	review.Status = status
	switch column {
	case "last_failed_build_id":
		review.LastFailedBuildID = buildID
	case "last_ready_build_id":
		review.LastReadyBuildID = buildID
	}
	updated, err := s.reviews.Update(ctx, review)
	if err != nil {
		return model.Review{}, err
	}
	if status == model.ReviewStatusReady {
		return updated, s.enqueueReview(ctx, updated)
	}
	return updated, s.reviews.DeleteMergeQueueEntryByReview(ctx, updated.ReviewID)
}

func (s *ReviewService) enqueueReview(ctx context.Context, review model.Review) error {
	if err := s.reviews.DeleteMergeQueueEntryByReview(ctx, review.ReviewID); err != nil {
		return err
	}
	_, err := s.reviews.CreateMergeQueueEntry(ctx, model.ReviewMergeQueueEntry{
		TargetBranch: review.TargetBranch,
		ReviewID:     review.ReviewID,
	})
	return err
}

// reviewLastBuildColumn names the last-build column a caller-reported status
// populates. MERGED has no case here: acceptMerged sets LastMergedBuildID
// itself once verifyGateBuild/verifyRepositoryState pass, rather than going
// through this generic buildID cross-check.
func reviewLastBuildColumn(status model.ReviewStatus) string {
	switch status {
	case model.ReviewStatusFailed:
		return "last_failed_build_id"
	case model.ReviewStatusReady:
		return "last_ready_build_id"
	default:
		return ""
	}
}
