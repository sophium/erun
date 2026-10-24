package platformclient

import (
	"context"
	"net/http"
	"time"
)

// PlatformPipelineJob is the job half of a pipeline item, matching the fields the console keeps.
type PlatformPipelineJob struct {
	JobID     string    `json:"jobId"`
	JobType   string    `json:"jobType"`
	IssueRef  string    `json:"issueRef,omitempty"`
	Summary   string    `json:"summary"`
	Status    string    `json:"status"`
	ActorKind string    `json:"actorKind"`
	ActorID   string    `json:"actorId"`
	StartedAt time.Time `json:"startedAt"`
	// EndedAt is nil while the job is PLANNED or RUNNING.
	EndedAt *time.Time `json:"endedAt,omitempty"`
}

// PlatformPipelineReview is the review half of a pipeline item.
type PlatformPipelineReview struct {
	ReviewID     string `json:"reviewId"`
	Repository   string `json:"repository,omitempty"`
	Name         string `json:"name"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
	Status       string `json:"status"`
}

// PlatformPipelineItem is one piece of work on one rung; exactly one of Job or Review is set.
type PlatformPipelineItem struct {
	// IssueKey is owner/repo#number, or empty when the work names no issue.
	IssueKey string `json:"issueKey"`
	// IssueRefSource lets a caller render an inferred link as inferred.
	IssueRef       string                  `json:"issueRef,omitempty"`
	IssueRefSource string                  `json:"issueRefSource,omitempty"`
	Rung           string                  `json:"rung"`
	Job            *PlatformPipelineJob    `json:"job,omitempty"`
	Review         *PlatformPipelineReview `json:"review,omitempty"`
}

// PlatformPipelineIssue groups items by issue; the keyless group comes last.
type PlatformPipelineIssue struct {
	IssueKey string                 `json:"issueKey"`
	Items    []PlatformPipelineItem `json:"items"`
}

// GetPipeline reads the tenant's whole pipeline, unfiltered, grouped by issue.
func (c *PlatformClient) GetPipeline(ctx context.Context) ([]PlatformPipelineIssue, error) {
	var issues []PlatformPipelineIssue
	err := c.do(ctx, http.MethodGet, "/v1/pipeline", nil, true, &issues)
	return issues, err
}
