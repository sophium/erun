package service

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
)

type ReviewRepository interface {
	Get(ctx context.Context, reviewID string) (model.Review, error)
	Update(ctx context.Context, review model.Review) (model.Review, error)
	FindNextMergeQueueReview(ctx context.Context, repository, targetBranch string) (model.Review, error)
	FindActiveMergeReview(ctx context.Context, repository, targetBranch string) (model.Review, error)
	// QueuedRepositories lets an unfiltered promotion refuse a queue spanning several repositories.
	QueuedRepositories(ctx context.Context, targetBranch string) (repository.MergeQueueRepositories, error)
	// FindLastMergedReview returns repository.ErrNotFound when nothing has merged onto
	// the branch through the queue yet.
	FindLastMergedReview(ctx context.Context, repository, targetBranch string) (model.Review, error)
	CreateMergeQueueEntry(ctx context.Context, entry model.ReviewMergeQueueEntry) (model.ReviewMergeQueueEntry, error)
	DeleteMergeQueueEntryByReview(ctx context.Context, reviewID string) error
}

type ReviewBuildRepository interface {
	// Get reads under the review's own tenant here.
	Get(ctx context.Context, tenantID, buildID string) (model.Build, error)
}

// MergeVerifier checks a reported merge commit against the real remote instead of
// trusting the MERGED caller.
type MergeVerifier interface {
	Contains(ctx context.Context, remoteURL, branch, commit string) (ok bool, parent string, err error)
	IsAncestor(ctx context.Context, remoteURL, branch, ancestor, descendant string) (isAncestor bool, err error)
	// ContainsChanges reports whether the source branch's work is already in the
	// target's history, for reviews that landed without the queue.
	ContainsChanges(ctx context.Context, remoteURL, targetBranch, sourceBranch string) (contained bool, commit string, err error)
}

type ReleaseTrigger interface {
	TriggerRelease(ctx context.Context, request ReleaseRequest) error
}

type ReviewCommentRepository interface {
	List(ctx context.Context, filter repository.CommentFilter) ([]model.Comment, error)
}

// ReviewAuditLogger is the raw audit repository, keeping the service free of HTTP types.
type ReviewAuditLogger interface {
	LogAuditEvent(ctx context.Context, event model.AuditEvent) error
}

// Must match routes.RegisterReviewRoutes exactly: audit records it as api_path
// and permission-by-path authorization keys on it.
const overrideAdvanceMergeQueueAPIPath = "/v1/reviews/merge-queue/override-advance"

// MERGE is never caller-settable; only AdvanceMergeQueue reaches it. MERGED is
// reachable from MERGE (merge-build verification) or from statuses whose work
// landed without the queue (reconcileMerged).
func validTargetsFor(status model.ReviewStatus) []model.ReviewStatus {
	switch status {
	case model.ReviewStatusOpen:
		return []model.ReviewStatus{model.ReviewStatusFailed, model.ReviewStatusReady, model.ReviewStatusMerged, model.ReviewStatusClosed}
	case model.ReviewStatusFailed:
		return []model.ReviewStatus{model.ReviewStatusReady, model.ReviewStatusMerged, model.ReviewStatusClosed}
	case model.ReviewStatusReady:
		return []model.ReviewStatus{model.ReviewStatusMerged, model.ReviewStatusClosed}
	case model.ReviewStatusMerge:
		return []model.ReviewStatus{model.ReviewStatusReady, model.ReviewStatusMerged}
	case model.ReviewStatusClosed, model.ReviewStatusMerged:
	}
	return nil
}

type ReviewService struct {
	reviews  ReviewRepository
	builds   ReviewBuildRepository
	comments ReviewCommentRepository
	audit    ReviewAuditLogger
	// A nil verifier refuses every MERGED report; a nil release skips the trigger.
	verifier MergeVerifier
	release  ReleaseTrigger
}

func NewReviewService(reviews ReviewRepository, builds ReviewBuildRepository, comments ReviewCommentRepository, audit ReviewAuditLogger, verifier MergeVerifier, release ReleaseTrigger) *ReviewService {
	return &ReviewService{reviews: reviews, builds: builds, comments: comments, audit: audit, verifier: verifier, release: release}
}

// PrepareCreate canonicalizes the repository so SSH and HTTPS remotes share one
// identity, and stores issue_ref in canonical owner/repo#number spelling.
func (s *ReviewService) PrepareCreate(review model.Review) (model.Review, error) {
	if review.Status == "" {
		review.Status = model.ReviewStatusOpen
	}
	repositoryIdentity, err := canonicalRepository(review.Repository)
	if err != nil {
		return model.Review{}, err
	}
	review.Repository = repositoryIdentity
	issueRef, err := modelpkg.NormalizeIssueRef(review.DeclaredIssueRef, repositoryIdentity)
	if err != nil {
		return model.Review{}, &InvalidIssueRefError{Reason: err.Error()}
	}
	review.DeclaredIssueRef = issueRef
	return review, nil
}

func canonicalRepository(repository string) (string, error) {
	if strings.TrimSpace(repository) == "" {
		return "", nil
	}
	identity, err := modelpkg.RepositoryIdentity(repository)
	if err != nil {
		return "", &InvalidRepositoryError{Reason: err.Error()}
	}
	return identity, nil
}

// AdvanceMergeQueue promotes the queue head to MERGE, refusing with
// UnresolvedThreadsError on open threads. The gate is server-side so no client can skip it.
func (s *ReviewService) AdvanceMergeQueue(ctx context.Context, repository, targetBranch string) (model.Review, error) {
	review, err := s.headOfMergeQueue(ctx, repository, targetBranch)
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

// OverrideAdvanceMergeQueue bypasses the thread gate; it fails closed without a
// reason or an audit logger.
func (s *ReviewService) OverrideAdvanceMergeQueue(ctx context.Context, repositoryIdentity, targetBranch, reason string) (model.Review, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return model.Review{}, repository.ErrInvalidInput
	}
	if s.audit == nil {
		return model.Review{}, errors.New("merge queue override requires audit logging, which is not configured on this control plane")
	}
	repositoryIdentity, err := canonicalRepository(repositoryIdentity)
	if err != nil {
		return model.Review{}, err
	}
	review, err := s.headOfMergeQueue(ctx, repositoryIdentity, targetBranch)
	if err != nil {
		return model.Review{}, err
	}
	if err := s.auditOverrideAdvance(ctx, review, reason); err != nil {
		return model.Review{}, err
	}
	return s.promoteToMerge(ctx, review)
}

// An empty repository means one queue across all the tenant's repositories,
// which covers reviews recorded before repository identity existed.
func (s *ReviewService) headOfMergeQueue(ctx context.Context, repositoryIdentity, targetBranch string) (model.Review, error) {
	if targetBranch == "" {
		return model.Review{}, ErrInvalidTargetBranch
	}
	if occupying, err := s.reviews.FindActiveMergeReview(ctx, repositoryIdentity, targetBranch); err == nil {
		return model.Review{}, &MergeQueueOccupiedError{
			TargetBranch: targetBranch,
			ReviewID:     occupying.ReviewID,
			Name:         occupying.Name,
			SourceBranch: occupying.SourceBranch,
		}
	} else if !errors.Is(err, repository.ErrNotFound) {
		return model.Review{}, err
	}
	review, err := s.reviews.FindNextMergeQueueReview(ctx, repositoryIdentity, targetBranch)
	if errors.Is(err, repository.ErrNotFound) {
		return model.Review{}, &EmptyMergeQueueError{TargetBranch: targetBranch}
	}
	if err != nil {
		return model.Review{}, err
	}
	if strings.TrimSpace(repositoryIdentity) == "" {
		if err := s.refuseAmbiguousQueue(ctx, targetBranch); err != nil {
			return model.Review{}, err
		}
	}
	return review, nil
}

// Reviews with no recorded repository don't count toward ambiguity, so a
// legacy tenant's single-repository queue still promotes.
func (s *ReviewService) refuseAmbiguousQueue(ctx context.Context, targetBranch string) error {
	queued, err := s.reviews.QueuedRepositories(ctx, targetBranch)
	if err != nil {
		return err
	}
	if len(queued.Named) < 2 {
		return nil
	}
	return &AmbiguousMergeQueueError{
		TargetBranch:        targetBranch,
		Repositories:        queued.Named,
		UnrecordedReviewIDs: queued.Unrecorded,
	}
}

func (s *ReviewService) promoteToMerge(ctx context.Context, review model.Review) (model.Review, error) {
	if err := s.reviews.DeleteMergeQueueEntryByReview(ctx, review.ReviewID); err != nil {
		return model.Review{}, err
	}
	review.Status = model.ReviewStatusMerge
	return s.reviews.Update(ctx, review)
}

// Mirrors review.CountUnresolvedThreads over the backend model to avoid
// importing the erun-common client type.
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

// UpdateStatus applies a caller-reported status transition; remoteURL is used
// only to verify a MERGED report.
func (s *ReviewService) UpdateStatus(ctx context.Context, reviewID string, status model.ReviewStatus, buildID string, remoteURL string) (model.Review, error) {
	review, err := s.reviews.Get(ctx, reviewID)
	if err != nil {
		return model.Review{}, err
	}

	if status == model.ReviewStatusMerge {
		return model.Review{}, &InvalidTransitionError{From: review.Status, To: status, ValidTargets: validTargetsFor(review.Status)}
	}

	// Which MERGED verification applies depends on the stored status, not the caller.
	if status == model.ReviewStatusMerged {
		repositoryIdentity, err := resolveReportedRepository(review, remoteURL)
		if err != nil {
			return model.Review{}, err
		}
		if review.Status == model.ReviewStatusMerge {
			return s.acceptMerged(ctx, review, buildID, remoteURL, repositoryIdentity)
		}
		return s.reconcileMerged(ctx, review, remoteURL, repositoryIdentity)
	}

	// READY without a build is the missed-merge-window path.
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

// The review is already MERGED, so a release dispatch failure is logged, not returned.
func (s *ReviewService) triggerRelease(ctx context.Context, review model.Review, commit string) {
	if s.release == nil {
		return
	}
	if err := s.release.TriggerRelease(ctx, ReleaseRequest{ReviewID: review.ReviewID, TargetBranch: review.TargetBranch, CommitID: commit}); err != nil {
		log.Printf("erun api reviews: triggering the release for review %s did not start: %v", review.ReviewID, err)
	}
}

func (s *ReviewService) requeueMergingReview(ctx context.Context, review model.Review) (model.Review, error) {
	if review.Status != model.ReviewStatusMerge {
		return model.Review{}, &ReviewNotMergingError{ReviewID: review.ReviewID, Status: review.Status}
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

// MarkBuildResult reports the review promoted to MERGE, which may differ from
// the build's own review: success only unblocks the target branch's queue.
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
	// A busy branch, an unresolved queue head, or an ambiguous queue is not a
	// failure of this build report; returning it would make a retry record a
	// second build row.
	promoted, err := s.AdvanceMergeQueue(ctx, updated.Repository, updated.TargetBranch)
	if err != nil {
		var blocked *UnresolvedThreadsError
		var occupied *MergeQueueOccupiedError
		var ambiguous *AmbiguousMergeQueueError
		if errors.Is(err, repository.ErrNotFound) || errors.As(err, &blocked) || errors.As(err, &occupied) || errors.As(err, &ambiguous) {
			return model.Review{}, false, nil
		}
		return model.Review{}, false, err
	}
	return promoted, true, nil
}

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
	build, err := s.builds.Get(ctx, review.TenantID, buildID)
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

// MERGED has no case: acceptMerged sets LastMergedBuildID after verification.
func reviewLastBuildColumn(status model.ReviewStatus) string {
	switch status {
	case model.ReviewStatusFailed:
		return "last_failed_build_id"
	case model.ReviewStatusReady:
		return "last_ready_build_id"
	case model.ReviewStatusOpen, model.ReviewStatusClosed, model.ReviewStatusMerge, model.ReviewStatusMerged:
	}
	return ""
}
