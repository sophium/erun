package main

import (
	"context"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

// loadTenantDashboardPipeline reads GET /v1/pipeline: jobs and reviews unioned by
// issue. It never adds rows to the merge queue.
func loadTenantDashboardPipeline(ctx context.Context, client *platformclient.PlatformClient, capabilities model.PlatformCapabilities, dashboard *uiTenantDashboard) {
	panel := uiTenantDashboardPanel{Tab: tenantDashboardTabPipeline}
	if restricted := restrictedTenantDashboardRead(capabilities, tenantDashboardReadPipeline); restricted != "" {
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

func tenantDashboardPipeline(issues []platformclient.PlatformPipelineIssue) []uiPipelineIssue {
	converted := make([]uiPipelineIssue, 0, len(issues))
	for _, issue := range issues {
		converted = append(converted, uiPipelineIssue{
			IssueKey: issue.IssueKey,
			Items:    tenantDashboardPipelineItems(issue.Items),
		})
	}
	return converted
}

func tenantDashboardPipelineItems(items []platformclient.PlatformPipelineItem) []uiPipelineItem {
	converted := make([]uiPipelineItem, 0, len(items))
	for _, item := range items {
		converted = append(converted, uiPipelineItem{
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
func tenantDashboardPipelineJob(job *platformclient.PlatformPipelineJob) *uiPipelineJob {
	if job == nil {
		return nil
	}
	return &uiPipelineJob{
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

func tenantDashboardPipelineReview(review *platformclient.PlatformPipelineReview) *uiPipelineReview {
	if review == nil {
		return nil
	}
	return &uiPipelineReview{
		ReviewID:     review.ReviewID,
		Repository:   review.Repository,
		Name:         review.Name,
		TargetBranch: review.TargetBranch,
		SourceBranch: review.SourceBranch,
		Status:       review.Status,
	}
}
