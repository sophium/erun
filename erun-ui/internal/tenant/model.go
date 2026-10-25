package tenant

import (
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

type DashboardInput struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment,omitempty"`
	MCPURL            string `json:"mcpUrl,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	// PlatformAlias empty defers to the sole configured erun alias; several yield choices.
	PlatformAlias string `json:"platformAlias,omitempty"`
	// Review filters resolve to the signed-in user's id server-side.
	ReviewFilterMine        bool `json:"reviewFilterMine,omitempty"`
	ReviewFilterWaitingOnMe bool `json:"reviewFilterWaitingOnMe,omitempty"`

	// MCPBearer is set server-side; json:"-" keeps it off the Wails boundary both ways.
	MCPBearer string `json:"-"`
}

type Dashboard struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment,omitempty"`
	APIURL      string `json:"apiUrl,omitempty"`
	// APIError is a whole-dashboard failure; a panel's own failure goes on the panel.
	APIError    string `json:"apiError,omitempty"`
	APILog      string `json:"apiLog,omitempty"`
	APILogError string `json:"apiLogError,omitempty"`
	// PlatformState is empty when the platform resolved (see StateReady).
	PlatformState        string   `json:"platformState,omitempty"`
	PlatformAliasChoices []string `json:"platformAliasChoices,omitempty"`
	PlatformAlias        string   `json:"platformAlias,omitempty"`
	PlatformURL          string   `json:"platformUrl,omitempty"`
	// PlatformIssuer/PlatformSubject are set even when the identity read failed.
	PlatformIssuer  string `json:"platformIssuer,omitempty"`
	PlatformSubject string `json:"platformSubject,omitempty"`
	// AccessRemedies maps a restricted route to the grant that would allow it.
	AccessRemedies map[string]platformclient.PlatformAccessRemedy `json:"accessRemedies,omitempty"`
	User           *DashboardUser                                 `json:"user,omitempty"`
	// Users is the tenant roster, distinct from the caller's own User.
	Users      []DashboardUser   `json:"users,omitempty"`
	Reviews    []DashboardReview `json:"reviews,omitempty"`
	MergeQueue []DashboardReview `json:"mergeQueue,omitempty"`
	Builds     []DashboardBuild  `json:"builds,omitempty"`
	// Pipeline groups jobs and reviews by issue; planned work appears only here.
	Pipeline             []PipelineIssue  `json:"pipeline,omitempty"`
	AuditEvents          []DashboardAudit `json:"auditEvents,omitempty"`
	Panels               []DashboardPanel `json:"panels,omitempty"`
	CanCreateReview      bool             `json:"canCreateReview"`
	CanAdvanceMergeQueue bool             `json:"canAdvanceMergeQueue"`
	// CanOverrideMergeQueue is a separate, usually narrower grant than CanAdvanceMergeQueue.
	CanOverrideMergeQueue bool `json:"canOverrideMergeQueue"`
	// Review counts are nil, not 0, when reviews or the user id are unreadable.
	MineReviewCount        *int `json:"mineReviewCount,omitempty"`
	WaitingOnMeReviewCount *int `json:"waitingOnMeReviewCount,omitempty"`
	// Contexts and Environments are platform objects, unrelated to local config,
	// and each degrades independently.
	Contexts               []PlatformContext     `json:"contexts,omitempty"`
	ContextsRestricted     string                `json:"contextsRestricted,omitempty"`
	ContextsError          string                `json:"contextsError,omitempty"`
	Environments           []PlatformEnvironment `json:"environments,omitempty"`
	EnvironmentsRestricted string                `json:"environmentsRestricted,omitempty"`
	EnvironmentsError      string                `json:"environmentsError,omitempty"`
	// Previewing an environment shares CanRegisterEnvironment's gate.
	CanCreateContext       bool `json:"canCreateContext"`
	CanRegisterEnvironment bool `json:"canRegisterEnvironment"`
	CanDeployEnvironment   bool `json:"canDeployEnvironment"`
	CanStopEnvironment     bool `json:"canStopEnvironment"`
	CanDeleteEnvironment   bool `json:"canDeleteEnvironment"`
	// MyInviteRequest is nil both when none exists and when the read failed;
	// MyInviteRequestError tells them apart.
	MyInviteRequest      *InviteRequest `json:"myInviteRequest,omitempty"`
	MyInviteRequestError string         `json:"myInviteRequestError,omitempty"`
	// PendingInviteRequestCount is nil, not 0, when the queue is unreadable.
	InviteRequests            []InviteRequest `json:"inviteRequests,omitempty"`
	PendingInviteRequestCount *int            `json:"pendingInviteRequestCount,omitempty"`
	CanApproveInviteRequests  bool            `json:"canApproveInviteRequests"`
	CanDeclineInviteRequests  bool            `json:"canDeclineInviteRequests"`
	// InviteRequestRateLimitWindowSeconds is best effort; 0 when unread.
	InviteRequestRateLimitWindowSeconds int `json:"inviteRequestRateLimitWindowSeconds,omitempty"`
}

// PlatformContext mirrors platformclient.PlatformContext.
type PlatformContext struct {
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

// PlatformEnvironment mirrors platformclient.PlatformEnvironment.
type PlatformEnvironment struct {
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

type CreatePlatformContextInput struct {
	Tenant             string `json:"tenant"`
	Name               string `json:"name"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType,omitempty"`
	DiskType           string `json:"diskType,omitempty"`
	DiskSizeGB         int    `json:"diskSizeGb,omitempty"`
	Preview            bool   `json:"preview,omitempty"`
}

// PlatformContextOutcome: "conflict"/"unavailable" are expected refusals, not errors.
type PlatformContextOutcome struct {
	Kind    string           `json:"kind"`
	Context *PlatformContext `json:"context,omitempty"`
	Plan    []string         `json:"plan,omitempty"`
	Message string           `json:"message,omitempty"`
}

// PlatformProvisionResult mirrors platformclient.PlatformProvisionResult.
type PlatformProvisionResult struct {
	Plan    []string `json:"plan"`
	QuotaOk bool     `json:"quotaOk"`
}

// RegisterPlatformEnvironmentInput backs both register and preview, so they cannot diverge.
type RegisterPlatformEnvironmentInput struct {
	Tenant            string `json:"tenant"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	ContextID         string `json:"contextId,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	Adopt             bool   `json:"adopt,omitempty"`
}

// PlatformEnvironmentActionInput.Version applies to deploy only.
type PlatformEnvironmentActionInput struct {
	Tenant        string `json:"tenant"`
	EnvironmentID string `json:"environmentId"`
	Version       string `json:"version,omitempty"`
}

// PlatformEnvironmentOutcome: "conflict"/"unavailable" are expected refusals, not errors.
type PlatformEnvironmentOutcome struct {
	Kind        string               `json:"kind"`
	Environment *PlatformEnvironment `json:"environment,omitempty"`
	Message     string               `json:"message,omitempty"`
}

// DashboardPanel separates Restricted (hidden) from Error (shown) per panel.
type DashboardPanel struct {
	Tab string `json:"tab"`
	// Restricted names the denied read in canonical form ("GET /v1/audit-events").
	Restricted string `json:"restricted,omitempty"`
	Error      string `json:"error,omitempty"`
}

type DashboardUser struct {
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

type DashboardReview struct {
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

type DashboardBuild struct {
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

type DashboardAudit struct {
	Type      string `json:"type"`
	Actor     string `json:"actor,omitempty"`
	Action    string `json:"action"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// PipelineIssue: an empty IssueKey groups work with no issue.
type PipelineIssue struct {
	IssueKey string         `json:"issueKey"`
	Items    []PipelineItem `json:"items"`
}

// PipelineItem has exactly one of Job or Review; Rung is the platform's
// label, never re-derived here.
type PipelineItem struct {
	IssueKey string `json:"issueKey"`
	// Empty IssueRefSource means the platform stated no link.
	IssueRef       string          `json:"issueRef,omitempty"`
	IssueRefSource string          `json:"issueRefSource,omitempty"`
	Rung           string          `json:"rung"`
	Job            *PipelineJob    `json:"job,omitempty"`
	Review         *PipelineReview `json:"review,omitempty"`
}

// PipelineJob has no branch, and nothing derives one.
type PipelineJob struct {
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

type PipelineReview struct {
	ReviewID     string `json:"reviewId"`
	Repository   string `json:"repository,omitempty"`
	Name         string `json:"name"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
	Status       string `json:"status"`
}

type ReviewDetailInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
}

// ReviewDetail's sub-reads degrade independently.
type ReviewDetail struct {
	ReviewID           string                                         `json:"reviewId"`
	APIError           string                                         `json:"apiError,omitempty"`
	Restricted         string                                         `json:"restricted,omitempty"`
	AccessRemedies     map[string]platformclient.PlatformAccessRemedy `json:"accessRemedies,omitempty"`
	Error              string                                         `json:"error,omitempty"`
	Review             *DashboardReview                               `json:"review,omitempty"`
	Comments           []ReviewComment                                `json:"comments,omitempty"`
	CommentsRestricted string                                         `json:"commentsRestricted,omitempty"`
	CommentsError      string                                         `json:"commentsError,omitempty"`
	Builds             []DashboardBuild                               `json:"builds,omitempty"`
	BuildsRestricted   string                                         `json:"buildsRestricted,omitempty"`
	BuildsError        string                                         `json:"buildsError,omitempty"`
	// QueuePosition is 1-based; 0 means not queued.
	QueuePosition int `json:"queuePosition,omitempty"`
	// UnresolvedThreads is valid only when Comments loaded.
	UnresolvedThreads   int        `json:"unresolvedThreads,omitempty"`
	CanComment          bool       `json:"canComment"`
	CanClose            bool       `json:"canClose"`
	CanResolveComments  bool       `json:"canResolveComments"`
	Reviewers           []Reviewer `json:"reviewers,omitempty"`
	ReviewersRestricted string     `json:"reviewersRestricted,omitempty"`
	ReviewersError      string     `json:"reviewersError,omitempty"`
	CanAssignReviewers  bool       `json:"canAssignReviewers"`
	CanRemoveReviewers  bool       `json:"canRemoveReviewers"`
	// AvailableReviewers constrains the picker so a cross-tenant userId cannot be submitted.
	AvailableReviewers []Reviewer `json:"availableReviewers,omitempty"`
}

type Reviewer struct {
	UserID   string `json:"userId"`
	Username string `json:"username,omitempty"`
}

type ReviewComment struct {
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

// UpdateReviewCommentStatusInput.CommentID must be a thread root.
type UpdateReviewCommentStatusInput struct {
	Tenant    string `json:"tenant"`
	ReviewID  string `json:"reviewId"`
	CommentID string `json:"commentId"`
}

// CreateReviewReplyInput copies its anchor from the parent comment.
type CreateReviewReplyInput struct {
	Tenant          string `json:"tenant"`
	ReviewID        string `json:"reviewId"`
	ParentCommentID string `json:"parentCommentId"`
	CommitID        string `json:"commitId"`
	FilePath        string `json:"filePath"`
	Line            int    `json:"line"`
	Body            string `json:"body"`
}

// CreateReviewInput's SourceBranch must already be pushed; without Repository
// the review cannot join a merge queue.
type CreateReviewInput struct {
	Tenant       string `json:"tenant"`
	Repository   string `json:"repository"`
	Name         string `json:"name"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
}

type CloseReviewInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
}

type AdvanceMergeQueueInput struct {
	Tenant string `json:"tenant"`
	// Repository and TargetBranch together name one queue; both are required.
	Repository   string `json:"repository"`
	TargetBranch string `json:"targetBranch"`
}

type OverrideAdvanceMergeQueueInput struct {
	Tenant       string `json:"tenant"`
	Repository   string `json:"repository"`
	TargetBranch string `json:"targetBranch"`
	// Reason is required and audited.
	Reason string `json:"reason"`
}

// AddReviewerInput.UserID must be enrolled in the caller's tenant.
type AddReviewerInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
	UserID   string `json:"userId"`
}

type RemoveReviewerInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
	UserID   string `json:"userId"`
}

// CreateReviewCommentInput starts a new top-level thread on a diff line.
type CreateReviewCommentInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
	CommitID string `json:"commitId"`
	FilePath string `json:"filePath"`
	Line     int    `json:"line"`
	Body     string `json:"body"`
}
