package eruncommon

import (
	"context"
	"net/http"
	"time"
)

// platform_client_pipeline.go extends PlatformClient with the pipeline view:
// the one read that unions planned and in-flight work with the review
// pipeline, grouped by the issue each piece of work belongs to and labelled
// by the rung it stands on.
//
// It is a read and only a read. The rungs are derived server-side from the
// status each owning pipeline already records, so nothing a caller does here
// moves work: the merge queue keeps its single meaning, the ordered set of
// READY reviews one gate drives.

// PlatformPipelineJob is the job half of a pipeline item. It is the subset
// every pipeline surface renders, deliberately the same fields the console's
// own parse keeps: two clients reading the same view with two different
// subsets is how one view of the pipeline stops being one view.
//
// IssueRef is stated by the actor that claimed the job, so it is declared
// whenever it is present at all -- nothing derives a job's issue from a
// branch.
type PlatformPipelineJob struct {
	JobID     string    `json:"jobId"`
	JobType   string    `json:"jobType"`
	IssueRef  string    `json:"issueRef,omitempty"`
	Summary   string    `json:"summary"`
	Status    string    `json:"status"`
	ActorKind string    `json:"actorKind"`
	ActorID   string    `json:"actorId"`
	StartedAt time.Time `json:"startedAt"`
	// EndedAt is set exactly when the job is no longer open -- no longer
	// PLANNED or RUNNING. A planned job therefore carries none, and a caller
	// must not read its absence as a job that failed to record one.
	EndedAt *time.Time `json:"endedAt,omitempty"`
}

// PlatformPipelineReview is the review half of a pipeline item.
//
// IssueRef/IssueRefSource are the review's resolved link and where it came
// from. A reference parsed out of a branch name is a guess about the branch,
// and IssueRefSource is the only thing that makes it tellable apart from one
// the author declared -- which is why it travels here rather than being
// dropped as decoration.
type PlatformPipelineReview struct {
	ReviewID     string `json:"reviewId"`
	Repository   string `json:"repository,omitempty"`
	Name         string `json:"name"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
	Status       string `json:"status"`
}

// PlatformPipelineItem is one piece of work on one rung, with the record it
// came from. Exactly one of Job or Review is set: an item is a job or a
// review, never both, and never neither. A row that could not say which it
// was would send an operator looking for a branch that does not exist (a job)
// or a job that does (a review).
type PlatformPipelineItem struct {
	// IssueKey is this item's issue in the canonical owner/repo#number
	// spelling, empty when the work names no issue the platform can key it
	// on. Empty is an answer, not a gap: work to show without an issue rather
	// than work to guess an issue for.
	IssueKey string `json:"issueKey"`
	// IssueRef and IssueRefSource are the item's own link and where it came
	// from, so a caller can render an inferred one as inferred.
	IssueRef       string                  `json:"issueRef,omitempty"`
	IssueRefSource string                  `json:"issueRefSource,omitempty"`
	Rung           string                  `json:"rung"`
	Job            *PlatformPipelineJob    `json:"job,omitempty"`
	Review         *PlatformPipelineReview `json:"review,omitempty"`
}

// PlatformPipelineIssue groups every item keyed on one issue. Items is never
// empty; IssueKey is empty for the single group of work that names no issue,
// which the platform orders last.
type PlatformPipelineIssue struct {
	IssueKey string                 `json:"issueKey"`
	Items    []PlatformPipelineItem `json:"items"`
}

// GetPipeline reads the caller's tenant's whole pipeline: every job and
// review the tenant has, unioned on the issue each belongs to.
//
// Unfiltered on purpose -- the view's point is the whole picture, and a
// narrowed one would be answering a different question. A tenant with nothing
// recorded answers with an empty list, which is a definite answer rather than
// a failure.
func (c *PlatformClient) GetPipeline(ctx context.Context) ([]PlatformPipelineIssue, error) {
	var issues []PlatformPipelineIssue
	err := c.do(ctx, http.MethodGet, "/v1/pipeline", nil, true, &issues)
	return issues, err
}
