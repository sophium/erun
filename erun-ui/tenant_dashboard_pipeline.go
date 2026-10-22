package main

import (
	"context"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// loadTenantDashboardPipeline is the Pipeline tab's own panel: every job and
// review this tenant has, unioned on the issue each belongs to. It is the
// desktop counterpart to the console's Pipeline section, reading the same
// GET /v1/pipeline.
//
// It spans the Reviews and Gates tabs rather than repeating them. A piece of
// planned work exists only here: it has a recorded issue and no branch at
// all, so neither the reviews list nor the merge queue can show it. The merge
// queue keeps its single meaning -- the ordered set of READY reviews one gate
// drives -- and nothing in this read adds a row to it.
//
// Degrades independently like every other panel here: a caller who cannot
// read GET /v1/pipeline gets a named restriction, and a read that fails
// carries its own error, neither of which blanks a neighbouring panel.
func loadTenantDashboardPipeline(ctx context.Context, client *eruncommon.PlatformClient, capabilities eruncommon.PlatformCapabilities, dashboard *uiTenantDashboard) {
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

func tenantDashboardPipeline(issues []eruncommon.PlatformPipelineIssue) []uiPipelineIssue {
	converted := make([]uiPipelineIssue, 0, len(issues))
	for _, issue := range issues {
		converted = append(converted, uiPipelineIssue{
			IssueKey: issue.IssueKey,
			Items:    tenantDashboardPipelineItems(issue.Items),
		})
	}
	return converted
}

func tenantDashboardPipelineItems(items []eruncommon.PlatformPipelineItem) []uiPipelineItem {
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

// tenantDashboardPipelineJob and tenantDashboardPipelineReview keep each half
// nil when the platform sent none. An item carries exactly one of the two,
// and a synthesised empty one would render as a record that exists but says
// nothing -- the opposite of the "which record is this" the row is for.
func tenantDashboardPipelineJob(job *eruncommon.PlatformPipelineJob) *uiPipelineJob {
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

// tenantDashboardOptionalTime renders a timestamp the platform may not have
// sent at all. Absent stays absent rather than becoming a zero instant a
// reader would take for a job that ended at the epoch.
func tenantDashboardOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return tenantDashboardTime(*value)
}

func tenantDashboardPipelineReview(review *eruncommon.PlatformPipelineReview) *uiPipelineReview {
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
