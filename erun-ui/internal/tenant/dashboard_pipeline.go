package tenant

import (
	"context"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// loadTenantDashboardPipeline reads GET /v1/pipeline: jobs and reviews unioned by
// issue. It never adds rows to the merge queue.
func loadTenantDashboardPipeline(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *Dashboard) {
	panel := DashboardPanel{Tab: tenantDashboardTabPipeline}
	if restricted := RestrictedRead(capabilities, tenantDashboardReadPipeline); restricted != "" {
		panel.Restricted = restricted
		dashboard.Panels = append(dashboard.Panels, panel)
		return
	}
	issues, err := client.GetPipeline(ctx)
	if err != nil {
		panel.Error = tenantDashboardReadError(tenantDashboardReadPipeline, err)
	} else {
		dashboard.Pipeline = tenantDashboardPipeline(issues)
	}
	dashboard.Panels = append(dashboard.Panels, panel)
}

func tenantDashboardPipeline(issues []platformclient.PlatformPipelineIssue) []PipelineIssue {
	converted := make([]PipelineIssue, 0, len(issues))
	for _, issue := range issues {
		converted = append(converted, PipelineIssue{
			IssueKey: issue.IssueKey,
			Items:    tenantDashboardPipelineItems(issue.Items),
		})
	}
	return converted
}

func tenantDashboardPipelineItems(items []platformclient.PlatformPipelineItem) []PipelineItem {
	converted := make([]PipelineItem, 0, len(items))
	for _, item := range items {
		converted = append(converted, PipelineItem{
			IssueKey:       item.IssueKey,
			IssueRef:       item.IssueRef,
			IssueRefSource: item.IssueRefSource,
			Rung:           item.Rung,
			Job:            tenantDashboardPipelineJob(item.Job),
			Review:         tenantDashboardPipelineReview(item.Review),
		})
	}
	return converted
}

// A missing half stays nil; an empty synthesised one would render as a blank record.
func tenantDashboardPipelineJob(job *platformclient.PlatformPipelineJob) *PipelineJob {
	if job == nil {
		return nil
	}
	return &PipelineJob{
		JobID:     job.JobID,
		JobType:   job.JobType,
		IssueRef:  job.IssueRef,
		Summary:   job.Summary,
		Status:    job.Status,
		ActorKind: job.ActorKind,
		ActorID:   job.ActorID,
		StartedAt: tenantDashboardTime(job.StartedAt),
		EndedAt:   tenantDashboardOptionalTime(job.EndedAt),
	}
}

// Absent stays "" rather than a zero instant.
func tenantDashboardOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return tenantDashboardTime(*value)
}

func tenantDashboardPipelineReview(review *platformclient.PlatformPipelineReview) *PipelineReview {
	if review == nil {
		return nil
	}
	return &PipelineReview{
		ReviewID:     review.ReviewID,
		Repository:   review.Repository,
		Name:         review.Name,
		TargetBranch: review.TargetBranch,
		SourceBranch: review.SourceBranch,
		Status:       review.Status,
	}
}
