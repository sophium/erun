package service

import (
	"context"
	"sort"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-common/base/core"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
)

// The pipeline view joins jobs and reviews by issue. It is read-only: it never
// adds rows to review_merge_queue, whose head a merge driver acts on.

// Pipeline rungs. Statuses outside the ladder (FAILED, ABANDONED) are reported
// as themselves, not folded into a rung.
const (
	rungPlanned    = "PLANNED"
	rungInProgress = "IN_PROGRESS"
	rungReviewOpen = "REVIEW_OPEN"
	rungReady      = "READY"
	rungMerging    = "MERGING"
	rungMerged     = "MERGED"
)

// Rungs absent from this map are outcomes and sort after the ladder.
var pipelineLadderOrder = map[string]int{
	rungPlanned:    0,
	rungInProgress: 1,
	rungReviewOpen: 2,
	rungReady:      3,
	rungMerging:    4,
	rungMerged:     5,
}

const rungAfterTheLadder = 100

// PipelineItem is one job or one review on a rung; exactly one of Job or Review is set.
type PipelineItem struct {
	// IssueKey is empty when the work names no issue; nothing is guessed.
	IssueKey string `json:"issueKey"`
	// IssueRefSource lets a caller render an inferred reference as inferred.
	IssueRef       string                        `json:"issueRef,omitempty"`
	IssueRefSource modelpkg.IssueReferenceSource `json:"issueRefSource,omitempty"`
	Rung           string                        `json:"rung"`
	Job            *model.Job                    `json:"job,omitempty"`
	Review         *model.Review                 `json:"review,omitempty"`
}

type PipelineIssue struct {
	// The unlinked group (empty IssueKey) is last and unique.
	IssueKey string         `json:"issueKey"`
	Items    []PipelineItem `json:"items"`
}

type PipelineJobLister interface {
	List(ctx context.Context, filter repository.JobFilter) ([]model.Job, error)
}

type PipelineReviewLister interface {
	List(ctx context.Context, filter repository.ReviewFilter) ([]model.Review, error)
}

// PipelineService builds the read-only pipeline view.
type PipelineService struct {
	jobs    PipelineJobLister
	reviews PipelineReviewLister
}

func NewPipelineService(jobs PipelineJobLister, reviews PipelineReviewLister) *PipelineService {
	return &PipelineService{jobs: jobs, reviews: reviews}
}

// Build returns the tenant's jobs and reviews grouped by issue, unlinked work last.
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

// GroupPipeline unions jobs and reviews onto their canonical issue keys.
func GroupPipeline(jobs []model.Job, reviews []model.Review) []PipelineIssue {
	grouped := map[string][]PipelineItem{}
	// keyOrder keeps output stable regardless of map iteration.
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
		// A job's reference is always stated by its claimer, never branch-derived.
		if item.IssueRef != "" {
			item.IssueRefSource = modelpkg.IssueReferenceDeclared
		}
		item.IssueKey = canonicalJobIssueKey(item.IssueRef)
		add(item.IssueKey, item)
	}

	for _, review := range reviews {
		item := PipelineItem{
			Rung:   reviewRung(review.Status),
			Review: &review,
		}
		// Same resolver as the review routes, so both agree on what is declared.
		if resolved, ok := core.ResolveIssueReference(review.DeclaredIssueRef, review.SourceBranch); ok {
			item.IssueRef = resolved.Ref
			item.IssueRefSource = resolved.Source
		}
		// Same key as jobs.issue_ref, which is what joins the two.
		item.IssueKey, _ = core.CanonicalIssueKey(item.IssueRef, review.Repository)
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
	sort.SliceStable(issues, func(i, j int) bool {
		if (issues[i].IssueKey == "") != (issues[j].IssueKey == "") {
			return issues[j].IssueKey == ""
		}
		return issues[i].IssueKey < issues[j].IssueKey
	})
	return issues
}

// A bare number in a job has no repository, so it yields no key.
func canonicalJobIssueKey(issueRef string) string {
	key, ok := core.CanonicalIssueKey(issueRef, "")
	if !ok {
		return ""
	}
	return key
}

func jobRung(status model.JobStatus) string {
	switch status {
	case model.JobStatusPlanned:
		return rungPlanned
	case model.JobStatusRunning:
		return rungInProgress
	case model.JobStatusSucceeded, model.JobStatusFailed, model.JobStatusAbandoned, model.JobStatusSuperseded:
	}
	return string(status)
}

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
	case model.ReviewStatusClosed, model.ReviewStatusFailed:
	}
	return string(status)
}

func pipelineRungSortKey(rung string) int {
	if order, ok := pipelineLadderOrder[rung]; ok {
		return order
	}
	return rungAfterTheLadder
}
