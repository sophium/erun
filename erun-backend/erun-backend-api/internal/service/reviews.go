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
	// QueuedRepositories reports what a target branch's queue is made of — the
	// repositories its waiting reviews name, and the waiting reviews that name
	// none — so a promotion that names none can refuse when that queue is
	// really several repositories'. See repository.MergeQueueRepositories.
	QueuedRepositories(ctx context.Context, targetBranch string) (repository.MergeQueueRepositories, error)
	// FindLastMergedReview is the platform's own record of what targetBranch's
	// tip was the last time a queue-driven merge landed on it — condition 2 of
	// accepting a MERGED report confirms a reported commit descends from it.
	// repository.ErrNotFound means no review has ever merged onto this
	// branch through the queue yet.
	FindLastMergedReview(ctx context.Context, repository, targetBranch string) (model.Review, error)
	CreateMergeQueueEntry(ctx context.Context, entry model.ReviewMergeQueueEntry) (model.ReviewMergeQueueEntry, error)
	DeleteMergeQueueEntryByReview(ctx context.Context, reviewID string) error
}

type ReviewBuildRepository interface {
	// Get takes the tenant that owns the read: the caller's own on the build
	// route, the review's own here, where every read is for a review this
	// service already holds. See repository.BuildRepository.Get.
	Get(ctx context.Context, tenantID, buildID string) (model.Build, error)
}

// MergeVerifier confirms a reported merge commit is really on the target
// branch's real remote, and that the work this review was gated against is
// still really its ancestor — the fact-about-the-repository check that
// replaces trusting whoever calls UpdateStatus with MERGED. See AGENTS.md
// "Merge Queue".
type MergeVerifier interface {
	Contains(ctx context.Context, remoteURL, branch, commit string) (ok bool, parent string, err error)
	IsAncestor(ctx context.Context, remoteURL, branch, ancestor, descendant string) (isAncestor bool, err error)
	// ContainsChanges is the other half of the same "is this work really in
	// the target" question, for a review that landed without the queue: it
	// answers whether everything the source branch adds is present in the
	// target's history, naming the commit that carries it. See
	// reconcileMerged.
	ContainsChanges(ctx context.Context, remoteURL, targetBranch, sourceBranch string) (contained bool, commit string, err error)
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

// validTargetsFor lists the statuses a caller's PATCH .../status may set from
// the review's current status, mirroring the Status lifecycle documented in
// collaboration/reviews.md. MERGE never appears: only AdvanceMergeQueue
// reaches it. MERGED appears from MERGE — where verifyMergeBuild and
// verifyRepositoryState have to pass — and from the three statuses a review
// can be sitting at while its work has already landed without the queue,
// where reconcileMerged's own check has to pass instead.
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
	// verifier and release are both optional: nil verifier refuses every
	// MERGED report (see verifyRepositoryState), and nil release simply
	// leaves an accepted merge's release un-triggered rather than erroring.
	verifier MergeVerifier
	release  ReleaseTrigger
}

func NewReviewService(reviews ReviewRepository, builds ReviewBuildRepository, comments ReviewCommentRepository, audit ReviewAuditLogger, verifier MergeVerifier, release ReleaseTrigger) *ReviewService {
	return &ReviewService{reviews: reviews, builds: builds, comments: comments, audit: audit, verifier: verifier, release: release}
}

// PrepareCreate normalizes a new review's status, repository identity, and
// declared issue reference. The repository is canonicalized here rather than
// trusted as sent: two clients holding SSH and HTTPS remotes for one
// repository must produce one identity, or each would find only its own
// reviews. An absent repository is left absent — a review of a repository
// with no nameable remote still records that honestly, and the queue reports
// it as unrecorded rather than guessing.
//
// The issue reference is normalized against the repository this same call
// just canonicalized, so a caller may state either the canonical
// owner/repo#number or the bare number the branch convention already teaches.
// Normalizing here rather than in the route keeps the stored column's
// contract in one place: what lands in reviews.issue_ref is always the
// canonical spelling, whichever transport wrote it.
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

// canonicalRepository canonicalizes a caller-supplied repository, leaving an
// absent one absent.
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

// AdvanceMergeQueue promotes targetBranch's queue head to MERGE, refusing
// (UnresolvedThreadsError) when that review still has open comment threads.
// The check is authoritative here, not only in a client: a caller that skips
// straight to this endpoint (the CLI, the API directly, a client bug) must
// meet the same bar a careful desktop user would. OverrideAdvanceMergeQueue is
// the one deliberate, audited way past it.
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

// OverrideAdvanceMergeQueue bypasses the unresolved-thread gate
// AdvanceMergeQueue enforces. It is the one legitimate escape from that gate,
// so it demands the two things that make a bypass accountable rather than a
// quiet workaround: a caller-stated reason, and a durable audit record of it.
// Both are required — a missing reason or an unconfigured audit logger fails
// closed rather than silently promoting anyway.
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

// headOfMergeQueue resolves the next review queued to merge into targetBranch
// in repository, refusing when another review is already merging on that
// branch. An empty repository is one queue spanning every repository the
// tenant serves — what a target branch alone has always meant, and what a
// review created before the platform recorded a repository belongs to.
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

// refuseAmbiguousQueue refuses a promotion from an unfiltered queue that is
// really several repositories' queues. A queue holding reviews from one
// repository — including the one queue every review created before the
// platform recorded a repository shares — still promotes exactly as it always
// has; only a genuinely mixed one, where "the head" names no single
// repository, is refused.
//
// Only the repositories the queued reviews actually name are counted. A review
// that records none names no repository to be one of several, so it neither
// makes a queue ambiguous nor resolves one: one named repository beside any
// number of unrecorded rows is still one repository's queue, which is what a
// tenant that predates repository identity has. Counting the absence as a
// second repository refused that tenant's queue outright, and the rows it
// refused to advance were the tenant's own.
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

	// MERGED has two verification stories, and which one applies is decided
	// by where the review is, not by what the caller claims: one holding
	// MERGE is the queue's, and is confirmed against its merge build; any
	// other review is one whose work landed without the queue, and is
	// confirmed against the target branch's own history.
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
	// succeeded. The queue advanced is the built review's own repository's, so
	// another repository's queue on the same target branch is unaffected.
	//
	// An ambiguous queue is the same shape of non-failure, and matters more
	// because nothing here resolves it: the build is already recorded and the
	// review has already gone READY, and the refusal is about which repository
	// a promotion that named none would be for. Returning it fails a report
	// that succeeded — a caller retrying creates a second build row for one
	// build — while the review sits READY where an explicit promotion naming a
	// repository can still reach it.
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

// reviewLastBuildColumn names the last-build column a caller-reported status
// populates. MERGED has no case here: acceptMerged sets LastMergedBuildID
// itself once verifyMergeBuild/verifyRepositoryState pass, rather than going
// through this generic buildID cross-check.
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
