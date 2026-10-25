package main

import (
	tenantsvc "github.com/sophium/erun/erun-ui/internal/tenant"
)

// The tenant platform surface; the behavior lives in internal/tenant.

func (a *App) AddReviewer(input tenantsvc.AddReviewerInput) (tenantsvc.Reviewer, error) {
	return a.tenant.AddReviewer(input)
}

func (a *App) AdvanceMergeQueue(input tenantsvc.AdvanceMergeQueueInput) (tenantsvc.DashboardReview, error) {
	return a.tenant.AdvanceMergeQueue(input)
}

func (a *App) ApproveTenantInviteRequest(input tenantsvc.DecideInviteRequestInput) (tenantsvc.InviteRequest, error) {
	return a.tenant.ApproveTenantInviteRequest(input)
}

func (a *App) CloseReview(input tenantsvc.CloseReviewInput) (tenantsvc.DashboardReview, error) {
	return a.tenant.CloseReview(input)
}

func (a *App) ConnectERunPlatform(input tenantsvc.ConnectERunPlatformInput) (uiCloudProviderStatus, error) {
	status, err := a.tenant.ConnectERunPlatform(input)
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(status), nil
}

func (a *App) CreatePlatformContext(input tenantsvc.CreatePlatformContextInput) (tenantsvc.PlatformContextOutcome, error) {
	return a.tenant.CreatePlatformContext(input)
}

func (a *App) CreateReview(input tenantsvc.CreateReviewInput) (tenantsvc.DashboardReview, error) {
	return a.tenant.CreateReview(input)
}

func (a *App) CreateReviewComment(input tenantsvc.CreateReviewCommentInput) (tenantsvc.ReviewComment, error) {
	return a.tenant.CreateReviewComment(input)
}

func (a *App) CreateReviewReply(input tenantsvc.CreateReviewReplyInput) (tenantsvc.ReviewComment, error) {
	return a.tenant.CreateReviewReply(input)
}

func (a *App) DeclineTenantInviteRequest(input tenantsvc.DeclineInviteRequestInput) (tenantsvc.InviteRequest, error) {
	return a.tenant.DeclineTenantInviteRequest(input)
}

func (a *App) DeletePlatformEnvironment(input tenantsvc.PlatformEnvironmentActionInput) (tenantsvc.PlatformEnvironmentOutcome, error) {
	return a.tenant.DeletePlatformEnvironment(input)
}

func (a *App) DeployPlatformEnvironment(input tenantsvc.PlatformEnvironmentActionInput) (tenantsvc.PlatformEnvironmentOutcome, error) {
	return a.tenant.DeployPlatformEnvironment(input)
}

func (a *App) EnrollERunPlatformUser(input tenantsvc.PlatformUserEnrollInput) (tenantsvc.PlatformUser, error) {
	return a.tenant.EnrollERunPlatformUser(input)
}

func (a *App) GetMyTenantInviteRequest(input tenantsvc.TenantInput) (*tenantsvc.InviteRequest, error) {
	return a.tenant.GetMyTenantInviteRequest(input)
}

func (a *App) ListTenantPlatformEnrollmentStatuses(input tenantsvc.ListEnrollmentStatusesInput) []tenantsvc.EnrollmentStatus {
	return a.tenant.ListTenantPlatformEnrollmentStatuses(input)
}

func (a *App) LoadReviewDetail(input tenantsvc.ReviewDetailInput) (tenantsvc.ReviewDetail, error) {
	return a.tenant.LoadReviewDetail(input)
}

func (a *App) LoadTenantDashboard(input tenantsvc.DashboardInput) (tenantsvc.Dashboard, error) {
	return a.tenant.LoadTenantDashboard(input)
}

func (a *App) OverrideAdvanceMergeQueue(input tenantsvc.OverrideAdvanceMergeQueueInput) (tenantsvc.DashboardReview, error) {
	return a.tenant.OverrideAdvanceMergeQueue(input)
}

func (a *App) PreviewPlatformEnvironment(input tenantsvc.RegisterPlatformEnvironmentInput) (tenantsvc.PlatformProvisionResult, error) {
	return a.tenant.PreviewPlatformEnvironment(input)
}

func (a *App) RegisterPlatformEnvironment(input tenantsvc.RegisterPlatformEnvironmentInput) (tenantsvc.PlatformEnvironmentOutcome, error) {
	return a.tenant.RegisterPlatformEnvironment(input)
}

func (a *App) RemoveReviewer(input tenantsvc.RemoveReviewerInput) error {
	return a.tenant.RemoveReviewer(input)
}

func (a *App) ResolveReviewComment(input tenantsvc.UpdateReviewCommentStatusInput) (tenantsvc.ReviewComment, error) {
	return a.tenant.ResolveReviewComment(input)
}

func (a *App) StopPlatformEnvironment(input tenantsvc.PlatformEnvironmentActionInput) (tenantsvc.PlatformEnvironmentOutcome, error) {
	return a.tenant.StopPlatformEnvironment(input)
}

func (a *App) SubmitTenantInviteRequest(input tenantsvc.SubmitInviteRequestInput) (tenantsvc.SubmitInviteRequestOutcome, error) {
	return a.tenant.SubmitTenantInviteRequest(input)
}

func (a *App) TenantReviewCreateCapability(tenantName string) (tenantsvc.ReviewCreateCapability, error) {
	return a.tenant.TenantReviewCreateCapability(tenantName)
}

func (a *App) UnresolveReviewComment(input tenantsvc.UpdateReviewCommentStatusInput) (tenantsvc.ReviewComment, error) {
	return a.tenant.UnresolveReviewComment(input)
}
