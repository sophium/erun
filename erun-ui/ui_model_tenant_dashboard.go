package main

import (
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

type uiTenantDashboardInput struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment,omitempty"`
	MCPURL            string `json:"mcpUrl,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	// PlatformAlias empty defers to the sole configured erun alias; several yield choices.
	PlatformAlias string `json:"platformAlias,omitempty"`
	// Review filters resolve to the signed-in user's id server-side.
	ReviewFilterMine        bool `json:"reviewFilterMine,omitempty"`
	ReviewFilterWaitingOnMe bool `json:"reviewFilterWaitingOnMe,omitempty"`

	// mcpBearer is set server-side and unexported so it never crosses the Wails boundary.
	mcpBearer string
}

type uiTenantDashboard struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment,omitempty"`
	APIURL      string `json:"apiUrl,omitempty"`
	// APIError is a whole-dashboard failure; a panel's own failure goes on the panel.
	APIError    string `json:"apiError,omitempty"`
	APILog      string `json:"apiLog,omitempty"`
	APILogError string `json:"apiLogError,omitempty"`
	// PlatformState is empty when the platform resolved (see tenantPlatformStateReady).
	PlatformState        string   `json:"platformState,omitempty"`
	PlatformAliasChoices []string `json:"platformAliasChoices,omitempty"`
	PlatformAlias        string   `json:"platformAlias,omitempty"`
	PlatformURL          string   `json:"platformUrl,omitempty"`
	// PlatformIssuer/PlatformSubject are set even when the identity read failed.
	PlatformIssuer  string `json:"platformIssuer,omitempty"`
	PlatformSubject string `json:"platformSubject,omitempty"`
	// AccessRemedies maps a restricted route to the grant that would allow it.
	AccessRemedies map[string]platformclient.PlatformAccessRemedy `json:"accessRemedies,omitempty"`
	User           *uiTenantDashboardUser                         `json:"user,omitempty"`
	// Users is the tenant roster, distinct from the caller's own User.
	Users      []uiTenantDashboardUser   `json:"users,omitempty"`
	Reviews    []uiTenantDashboardReview `json:"reviews,omitempty"`
	MergeQueue []uiTenantDashboardReview `json:"mergeQueue,omitempty"`
	Builds     []uiTenantDashboardBuild  `json:"builds,omitempty"`
	// Pipeline groups jobs and reviews by issue; planned work appears only here.
	Pipeline             []uiPipelineIssue        `json:"pipeline,omitempty"`
	AuditEvents          []uiTenantDashboardAudit `json:"auditEvents,omitempty"`
	Panels               []uiTenantDashboardPanel `json:"panels,omitempty"`
	CanCreateReview      bool                     `json:"canCreateReview"`
	CanAdvanceMergeQueue bool                     `json:"canAdvanceMergeQueue"`
	// CanOverrideMergeQueue is a separate, usually narrower grant than CanAdvanceMergeQueue.
	CanOverrideMergeQueue bool `json:"canOverrideMergeQueue"`
	// Review counts are nil, not 0, when reviews or the user id are unreadable.
	MineReviewCount        *int `json:"mineReviewCount,omitempty"`
	WaitingOnMeReviewCount *int `json:"waitingOnMeReviewCount,omitempty"`
	// Contexts and Environments are platform objects, unrelated to local config,
	// and each degrades independently.
	Contexts               []uiPlatformContext     `json:"contexts,omitempty"`
	ContextsRestricted     string                  `json:"contextsRestricted,omitempty"`
	ContextsError          string                  `json:"contextsError,omitempty"`
	Environments           []uiPlatformEnvironment `json:"environments,omitempty"`
	EnvironmentsRestricted string                  `json:"environmentsRestricted,omitempty"`
	EnvironmentsError      string                  `json:"environmentsError,omitempty"`
	// Previewing an environment shares CanRegisterEnvironment's gate.
	CanCreateContext       bool `json:"canCreateContext"`
	CanRegisterEnvironment bool `json:"canRegisterEnvironment"`
	CanDeployEnvironment   bool `json:"canDeployEnvironment"`
	CanStopEnvironment     bool `json:"canStopEnvironment"`
	CanDeleteEnvironment   bool `json:"canDeleteEnvironment"`
	// MyInviteRequest is nil both when none exists and when the read failed;
	// MyInviteRequestError tells them apart.
	MyInviteRequest      *uiInviteRequest `json:"myInviteRequest,omitempty"`
	MyInviteRequestError string           `json:"myInviteRequestError,omitempty"`
	// PendingInviteRequestCount is nil, not 0, when the queue is unreadable.
	InviteRequests            []uiInviteRequest `json:"inviteRequests,omitempty"`
	PendingInviteRequestCount *int              `json:"pendingInviteRequestCount,omitempty"`
	CanApproveInviteRequests  bool              `json:"canApproveInviteRequests"`
	CanDeclineInviteRequests  bool              `json:"canDeclineInviteRequests"`
	// InviteRequestRateLimitWindowSeconds is best effort; 0 when unread.
	InviteRequestRateLimitWindowSeconds int `json:"inviteRequestRateLimitWindowSeconds,omitempty"`
}

// uiPlatformContext mirrors platformclient.PlatformContext.
type uiPlatformContext struct {
	ContextID          string `json:"contextId"`
	Name               string `json:"name"`
	Provider           string `json:"provider"`
	CloudProviderAlias string `json:"cloudProviderAlias,omitempty"`
	Region             string `json:"region,omitempty"`
	InstanceType       string `json:"instanceType,omitempty"`
	KubernetesContext  string `json:"kubernetesContext,omitempty"`
	Status             string `json:"status"`
	ProvisionError     string `json:"provisionError,omitempty"`
}

// uiPlatformEnvironment mirrors platformclient.PlatformEnvironment.
type uiPlatformEnvironment struct {
	EnvironmentID     string `json:"environmentId"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	ContextID         string `json:"contextId,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	Status            string `json:"status"`
	ProvisionError    string `json:"provisionError,omitempty"`
	DeployedVersion   string `json:"deployedVersion,omitempty"`
	DeleteError       string `json:"deleteError,omitempty"`
}

type uiCreatePlatformContextInput struct {
	Tenant             string `json:"tenant"`
	Name               string `json:"name"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType,omitempty"`
	DiskType           string `json:"diskType,omitempty"`
	DiskSizeGB         int    `json:"diskSizeGb,omitempty"`
	Preview            bool   `json:"preview,omitempty"`
}

// uiPlatformContextOutcome: "conflict"/"unavailable" are expected refusals, not errors.
type uiPlatformContextOutcome struct {
	Kind    string             `json:"kind"`
	Context *uiPlatformContext `json:"context,omitempty"`
	Plan    []string           `json:"plan,omitempty"`
	Message string             `json:"message,omitempty"`
}

// uiPlatformProvisionResult mirrors platformclient.PlatformProvisionResult.
type uiPlatformProvisionResult struct {
	Plan    []string `json:"plan"`
	QuotaOk bool     `json:"quotaOk"`
}

// uiRegisterPlatformEnvironmentInput backs both register and preview, so they cannot diverge.
type uiRegisterPlatformEnvironmentInput struct {
	Tenant            string `json:"tenant"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	ContextID         string `json:"contextId,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	Adopt             bool   `json:"adopt,omitempty"`
}

// uiPlatformEnvironmentActionInput.Version applies to deploy only.
type uiPlatformEnvironmentActionInput struct {
	Tenant        string `json:"tenant"`
	EnvironmentID string `json:"environmentId"`
	Version       string `json:"version,omitempty"`
}

// uiPlatformEnvironmentOutcome: "conflict"/"unavailable" are expected refusals, not errors.
type uiPlatformEnvironmentOutcome struct {
	Kind        string                 `json:"kind"`
	Environment *uiPlatformEnvironment `json:"environment,omitempty"`
	Message     string                 `json:"message,omitempty"`
}

// uiTenantDashboardPanel separates Restricted (hidden) from Error (shown) per panel.
type uiTenantDashboardPanel struct {
	Tab string `json:"tab"`
	// Restricted names the denied read in canonical form ("GET /v1/audit-events").
	Restricted string `json:"restricted,omitempty"`
	Error      string `json:"error,omitempty"`
}

type uiTenantDashboardUser struct {
	TenantID string `json:"tenantId"`
	// TenantName is set on the caller's own row only.
	TenantName string   `json:"tenantName,omitempty"`
	UserID     string   `json:"userId"`
	Username   string   `json:"username,omitempty"`
	Roles      []string `json:"roles,omitempty"`
	Issuer     string   `json:"issuer,omitempty"`
	Subject    string   `json:"subject,omitempty"`
	CreatedAt  string   `json:"createdAt,omitempty"`
	UpdatedAt  string   `json:"updatedAt,omitempty"`
}

type uiTenantDashboardReview struct {
	ReviewID     string `json:"reviewId"`
	TenantID     string `json:"tenantId"`
	AuthorUserID string `json:"authorUserId,omitempty"`
	// AuthorUsername is best effort; empty falls back to the raw id.
	AuthorUsername string `json:"authorUsername,omitempty"`
	Name           string `json:"name"`
	// Repository is empty for reviews created before the platform recorded one.
	Repository   string `json:"repository,omitempty"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
	Status       string `json:"status"`
	// UnresolvedThreads is nil when not computed for this listing.
	UnresolvedThreads *int `json:"unresolvedThreads,omitempty"`
	// Blocked is set only by AdvanceMergeQueue's refusal.
	Blocked           bool   `json:"blocked,omitempty"`
	LastFailedBuildID string `json:"lastFailedBuildId,omitempty"`
	LastReadyBuildID  string `json:"lastReadyBuildId,omitempty"`
	LastMergedBuildID string `json:"lastMergedBuildId,omitempty"`
	CreatedAt         string `json:"createdAt,omitempty"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
}

type uiTenantDashboardBuild struct {
	BuildID         string                     `json:"buildId"`
	TenantID        string                     `json:"tenantId"`
	ReviewID        string                     `json:"reviewId,omitempty"`
	ReviewName      string                     `json:"reviewName,omitempty"`
	EnvironmentID   string                     `json:"environmentId,omitempty"`
	EnvironmentName string                     `json:"environmentName,omitempty"`
	Successful      bool                       `json:"successful"`
	CommitID        string                     `json:"commitId"`
	Version         string                     `json:"version"`
	Profile         *model.BuildProfileSummary `json:"profile,omitempty"`
	CreatedAt       string                     `json:"createdAt,omitempty"`
	UpdatedAt       string                     `json:"updatedAt,omitempty"`
}

type uiTenantDashboardAudit struct {
	Type      string `json:"type"`
	Actor     string `json:"actor,omitempty"`
	Action    string `json:"action"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// uiPipelineIssue: an empty IssueKey groups work with no issue.
type uiPipelineIssue struct {
	IssueKey string           `json:"issueKey"`
	Items    []uiPipelineItem `json:"items"`
}

// uiPipelineItem has exactly one of Job or Review; Rung is the platform's
// label, never re-derived here.
type uiPipelineItem struct {
	IssueKey string `json:"issueKey"`
	// Empty IssueRefSource means the platform stated no link.
	IssueRef       string            `json:"issueRef,omitempty"`
	IssueRefSource string            `json:"issueRefSource,omitempty"`
	Rung           string            `json:"rung"`
	Job            *uiPipelineJob    `json:"job,omitempty"`
	Review         *uiPipelineReview `json:"review,omitempty"`
}

// uiPipelineJob has no branch, and nothing derives one.
type uiPipelineJob struct {
	JobID     string `json:"jobId"`
	JobType   string `json:"jobType"`
	IssueRef  string `json:"issueRef,omitempty"`
	Summary   string `json:"summary"`
	Status    string `json:"status"`
	ActorKind string `json:"actorKind"`
	ActorID   string `json:"actorId"`
	StartedAt string `json:"startedAt,omitempty"`
	EndedAt   string `json:"endedAt,omitempty"`
}

type uiPipelineReview struct {
	ReviewID     string `json:"reviewId"`
	Repository   string `json:"repository,omitempty"`
	Name         string `json:"name"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
	Status       string `json:"status"`
}

type uiReviewDetailInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
}

// uiReviewDetail's sub-reads degrade independently.
type uiReviewDetail struct {
	ReviewID           string                                         `json:"reviewId"`
	APIError           string                                         `json:"apiError,omitempty"`
	Restricted         string                                         `json:"restricted,omitempty"`
	AccessRemedies     map[string]platformclient.PlatformAccessRemedy `json:"accessRemedies,omitempty"`
	Error              string                                         `json:"error,omitempty"`
	Review             *uiTenantDashboardReview                       `json:"review,omitempty"`
	Comments           []uiReviewComment                              `json:"comments,omitempty"`
	CommentsRestricted string                                         `json:"commentsRestricted,omitempty"`
	CommentsError      string                                         `json:"commentsError,omitempty"`
	Builds             []uiTenantDashboardBuild                       `json:"builds,omitempty"`
	BuildsRestricted   string                                         `json:"buildsRestricted,omitempty"`
	BuildsError        string                                         `json:"buildsError,omitempty"`
	// QueuePosition is 1-based; 0 means not queued.
	QueuePosition int `json:"queuePosition,omitempty"`
	// UnresolvedThreads is valid only when Comments loaded.
	UnresolvedThreads   int          `json:"unresolvedThreads,omitempty"`
	CanComment          bool         `json:"canComment"`
	CanClose            bool         `json:"canClose"`
	CanResolveComments  bool         `json:"canResolveComments"`
	Reviewers           []uiReviewer `json:"reviewers,omitempty"`
	ReviewersRestricted string       `json:"reviewersRestricted,omitempty"`
	ReviewersError      string       `json:"reviewersError,omitempty"`
	CanAssignReviewers  bool         `json:"canAssignReviewers"`
	CanRemoveReviewers  bool         `json:"canRemoveReviewers"`
	// AvailableReviewers constrains the picker so a cross-tenant userId cannot be submitted.
	AvailableReviewers []uiReviewer `json:"availableReviewers,omitempty"`
}

type uiReviewer struct {
	UserID   string `json:"userId"`
	Username string `json:"username,omitempty"`
}

type uiReviewComment struct {
	CommentID       string `json:"commentId"`
	CreatorUserID   string `json:"creatorUserId,omitempty"`
	CreatorUsername string `json:"creatorUsername,omitempty"`
	Status          string `json:"status"`
	ParentCommentID string `json:"parentCommentId,omitempty"`
	CommitID        string `json:"commitId"`
	FilePath        string `json:"filePath"`
	Line            int    `json:"line"`
	Body            string `json:"body"`
	CreatedAt       string `json:"createdAt,omitempty"`
}

// uiUpdateReviewCommentStatusInput.CommentID must be a thread root.
type uiUpdateReviewCommentStatusInput struct {
	Tenant    string `json:"tenant"`
	ReviewID  string `json:"reviewId"`
	CommentID string `json:"commentId"`
}

// uiCreateReviewReplyInput copies its anchor from the parent comment.
type uiCreateReviewReplyInput struct {
	Tenant          string `json:"tenant"`
	ReviewID        string `json:"reviewId"`
	ParentCommentID string `json:"parentCommentId"`
	CommitID        string `json:"commitId"`
	FilePath        string `json:"filePath"`
	Line            int    `json:"line"`
	Body            string `json:"body"`
}

// uiCreateReviewInput's SourceBranch must already be pushed; without Repository
// the review cannot join a merge queue.
type uiCreateReviewInput struct {
	Tenant       string `json:"tenant"`
	Repository   string `json:"repository"`
	Name         string `json:"name"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
}

type uiCloseReviewInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
}

type uiAdvanceMergeQueueInput struct {
	Tenant string `json:"tenant"`
	// Repository and TargetBranch together name one queue; both are required.
	Repository   string `json:"repository"`
	TargetBranch string `json:"targetBranch"`
}

type uiOverrideAdvanceMergeQueueInput struct {
	Tenant       string `json:"tenant"`
	Repository   string `json:"repository"`
	TargetBranch string `json:"targetBranch"`
	// Reason is required and audited.
	Reason string `json:"reason"`
}

// uiAddReviewerInput.UserID must be enrolled in the caller's tenant.
type uiAddReviewerInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
	UserID   string `json:"userId"`
}

type uiRemoveReviewerInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
	UserID   string `json:"userId"`
}

// uiCreateReviewCommentInput starts a new top-level thread on a diff line.
type uiCreateReviewCommentInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
	CommitID string `json:"commitId"`
	FilePath string `json:"filePath"`
	Line     int    `json:"line"`
	Body     string `json:"body"`
}
