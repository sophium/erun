package service

import (
	"context"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	eruncommon "github.com/sophium/erun/erun-common"
)

// pipeline.go answers "what is going on?" in one read, across the two
// pipelines erun keeps work in.
//
// Work erun tracks does not live in one table. A unit of planned work is a
// job -- it carries the issue reference, the owning actor, and its own status
// ladder. A unit of work being proposed for merge is a review, with its own
// ladder (OPEN -> READY -> MERGE -> MERGED) driven by the merge queue. Until
// this, an operator watching a change go from idea to merged had to read two
// surfaces and join them by eye, which is what made "what is going on"
// unanswerable in one place.
//
// This is a read and nothing else. It changes no state machine, adds no rows
// to review_merge_queue, and moves no review: that table is an eligibility
// list whose head query a merge driver acts on, so a planned row placed there
// would hand a driver a branch that does not exist. Planned work is visible
// here, beside the queue, never in it.

// The pipeline's own rungs: the steps a piece of work passes through on its
// way from an idea to a merged change. Each is derived from the status the
// owning pipeline already records -- no new state is introduced by having a
// name for the rung it is on.
//
// A status outside this ladder (a failed review, a superseded job) is
// reported as itself rather than folded into one of these: "FAILED" and
// "ABANDONED" are outcomes an operator acts on differently from any rung of
// the ladder, and a view that renamed them would be hiding the answer it
// exists to give.
const (
	rungPlanned    = "PLANNED"
	rungInProgress = "IN_PROGRESS"
	rungReviewOpen = "REVIEW_OPEN"
	rungReady      = "READY"
	rungMerging    = "MERGING"
	rungMerged     = "MERGED"
)

// pipelineLadderOrder is the order the rungs are presented in, lowest first.
// A rung absent from this map is an outcome rather than a step and sorts
// after every rung of the ladder.
var pipelineLadderOrder = map[string]int{
	rungPlanned:    0,
	rungInProgress: 1,
	rungReviewOpen: 2,
	rungReady:      3,
	rungMerging:    4,
	rungMerged:     5,
}

// rungAfterTheLadder keeps every outcome behind every step, and behind each
// other in a stable order a caller can rely on.
const rungAfterTheLadder = 100

// PipelineItem is one piece of work on one rung, with the record it came from.
// Exactly one of Job or Review is set: an item is a job or a review, never
// both, and never neither.
type PipelineItem struct {
	// IssueKey is this item's issue in the canonical owner/repo#number
	// spelling, empty when the work names no issue the platform can key it
	// on. Empty is an answer, not a gap: it is work to show without an issue
	// rather than work to guess an issue for.
	IssueKey string `json:"issueKey"`
	// IssueRef and IssueRefSource are the item's own link and where it came
	// from, so a caller can render an inferred one as inferred. A job's
	// reference is stated by whoever claimed the job; a review's is what its
	// author declared, or else the number its branch names.
	IssueRef       string                          `json:"issueRef,omitempty"`
	IssueRefSource eruncommon.IssueReferenceSource `json:"issueRefSource,omitempty"`
	Rung           string                          `json:"rung"`
	Job            *model.Job                      `json:"job,omitempty"`
	Review         *model.Review                   `json:"review,omitempty"`
}

// PipelineIssue groups every item keyed on one issue. Items is never empty.
type PipelineIssue struct {
	// IssueKey is the canonical key these items share, empty for the group of
	// work that names no issue. The unlinked group is last, and there is at
	// most one of it.
	IssueKey string         `json:"issueKey"`
	Items    []PipelineItem `json:"items"`
}

type PipelineJobLister interface {
	List(ctx context.Context, filter repository.JobFilter) ([]model.Job, error)
}

type PipelineReviewLister interface {
	List(ctx context.Context, filter repository.ReviewFilter) ([]model.Review, error)
}

// PipelineService builds the pipeline view. It reads and only reads.
type PipelineService struct {
	jobs    PipelineJobLister
	reviews PipelineReviewLister
}

func NewPipelineService(jobs PipelineJobLister, reviews PipelineReviewLister) *PipelineService {
	return &PipelineService{jobs: jobs, reviews: reviews}
}

// Build returns every job and review the caller's tenant has, grouped by the
// issue each belongs to and ordered by the rung each stands on.
//
// Both reads are unfiltered: the view's whole purpose is the whole picture,
// and a caller narrowing it would be answering a different question. The
// lists are ordered by issue key, with the work that names no issue last.
func (s *PipelineService) Build(ctx context.Context) ([]PipelineIssue, error) {
	jobs, err := s.jobs.List(ctx, repository.JobFilter{})
	if err != nil {
		return nil, err
	}
	reviews, err := s.reviews.List(ctx, repository.ReviewFilter{})
	if err != nil {
		return nil, err
	}
	return GroupPipeline(jobs, reviews), nil
}

// GroupPipeline unions jobs and reviews onto the canonical issue key each
// belongs to. It is a pure function of its two inputs so the union can be
// exercised without a database, and so the same join is available to any
// caller that already holds both lists.
func GroupPipeline(jobs []model.Job, reviews []model.Review) []PipelineIssue {
	grouped := map[string][]PipelineItem{}
	// keyOrder keeps the groups in the order their first item arrived, so a
	// run over the same rows is stable without depending on map iteration.
	var keyOrder []string
	add := func(key string, item PipelineItem) {
		if _, seen := grouped[key]; !seen {
			keyOrder = append(keyOrder, key)
		}
		grouped[key] = append(grouped[key], item)
	}

	for _, job := range jobs {
		item := PipelineItem{
			Rung:     jobRung(job.Status),
			Job:      &job,
			IssueRef: strings.TrimSpace(job.IssueRef),
		}
		// A job's reference is stated by the actor that claimed it — nothing
		// derives one from a branch — so it is declared whenever it is
		// present at all.
		if item.IssueRef != "" {
			item.IssueRefSource = eruncommon.IssueReferenceDeclared
		}
		item.IssueKey = canonicalJobIssueKey(item.IssueRef)
		add(item.IssueKey, item)
	}

	for _, review := range reviews {
		item := PipelineItem{
			Rung:   reviewRung(review.Status),
			Review: &review,
		}
		// Resolved through the same shared resolver the review routes use, so
		// a link this view calls declared is exactly one they would.
		if resolved, ok := eruncommon.ResolveIssueReference(review.DeclaredIssueRef, review.SourceBranch); ok {
			item.IssueRef = resolved.Ref
			item.IssueRefSource = resolved.Source
		}
		// The join: a declared reference already names its repository and is
		// taken verbatim; a number parsed out of a branch is addressed to the
		// repository the review recorded. Either way this is the same key
		// jobs.issue_ref holds, which is what makes one view of both possible.
		item.IssueKey, _ = eruncommon.CanonicalIssueKey(item.IssueRef, review.Repository)
		add(item.IssueKey, item)
	}

	issues := make([]PipelineIssue, 0, len(keyOrder))
	for _, key := range keyOrder {
		items := grouped[key]
		sort.SliceStable(items, func(i, j int) bool {
			return pipelineRungSortKey(items[i].Rung) < pipelineRungSortKey(items[j].Rung)
		})
		issues = append(issues, PipelineIssue{IssueKey: key, Items: items})
	}
	// Named issues first, in a stable order; the work that names no issue is
	// deliberately last, since it is the group an operator reads after the
	// ones they were looking for.
	sort.SliceStable(issues, func(i, j int) bool {
		if (issues[i].IssueKey == "") != (issues[j].IssueKey == "") {
			return issues[j].IssueKey == ""
		}
		return issues[i].IssueKey < issues[j].IssueKey
	})
	return issues
}

// canonicalJobIssueKey answers the canonical key for a job's own issue_ref.
// A job states its reference in the canonical spelling already, so the second
// argument carries no repository: a bare number written into a job is not
// joined to anything, and yields no key rather than a guessed one.
func canonicalJobIssueKey(issueRef string) string {
	key, ok := eruncommon.CanonicalIssueKey(issueRef, "")
	if !ok {
		return ""
	}
	return key
}

// jobRung names the rung a job stands on. PLANNED and RUNNING are the
// ladder's first two steps; every other status is an outcome and is reported
// as itself.
func jobRung(status model.JobStatus) string {
	switch status {
	case model.JobStatusPlanned:
		return rungPlanned
	case model.JobStatusRunning:
		return rungInProgress
	default:
		return string(status)
	}
}

// reviewRung names the rung a review stands on. OPEN is work being written,
// READY is queued behind a target branch's gate, MERGE is the queue's single
// in-flight slot, and MERGED is landed. CLOSED and FAILED are outcomes and
// are reported as themselves.
func reviewRung(status model.ReviewStatus) string {
	switch status {
	case model.ReviewStatusOpen:
		return rungReviewOpen
	case model.ReviewStatusReady:
		return rungReady
	case model.ReviewStatusMerge:
		return rungMerging
	case model.ReviewStatusMerged:
		return rungMerged
	default:
		return string(status)
	}
}

func pipelineRungSortKey(rung string) int {
	if order, ok := pipelineLadderOrder[rung]; ok {
		return order
	}
	return rungAfterTheLadder
}
