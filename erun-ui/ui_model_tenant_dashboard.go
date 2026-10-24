package main

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

type uiTenantDashboardInput struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment,omitempty"`
	MCPURL            string `json:"mcpUrl,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	// PlatformAlias optionally names which configured erun-type cloud alias's
	// platform to read. Empty defers to the caller's sole configured erun
	// alias; when more than one is configured the load reports every alias as
	// a choice instead of guessing. See tenant_platform.go — the desktop
	// resolves the platform's base URL and identity the same way `erun
	// platform` does, never from an environment's own apiUrl.
	PlatformAlias string `json:"platformAlias,omitempty"`
	// ReviewFilterMine/ReviewFilterWaitingOnMe are the Reviews tab's one-click
	// discovery filters. Both resolve to the signed-in user's own id (already
	// known from this same load's whoami call) rather than asking the
	// frontend to carry a user id it has no other reason to know.
	ReviewFilterMine        bool `json:"reviewFilterMine,omitempty"`
	ReviewFilterWaitingOnMe bool `json:"reviewFilterWaitingOnMe,omitempty"`

	// mcpBearer is the per-env MCP edge token for the dashboard's MCP API-log
	// read. Set server-side from the desktop identity, never by the frontend;
	// unexported so the secret never crosses the Wails boundary.
	mcpBearer string
}

type uiTenantDashboard struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment,omitempty"`
	APIURL      string `json:"apiUrl,omitempty"`
	// APIError is a whole-dashboard failure: the caller's identity could not be
	// read, so no panel can be resolved or gated. A single panel's own failure
	// belongs on that panel, not here.
	APIError    string `json:"apiError,omitempty"`
	APILog      string `json:"apiLog,omitempty"`
	APILogError string `json:"apiLogError,omitempty"`
	// PlatformState names why the platform identity is not ready to load, so
	// the frontend renders the one action that resolves it instead of a
	// generic error. "" means the platform resolved and the load proceeded
	// (see tenantPlatformStateReady and its siblings in tenant_platform.go).
	PlatformState string `json:"platformState,omitempty"`
	// PlatformAliasChoices lists every configured erun-type alias when more
	// than one exists and the caller did not name one to use.
	PlatformAliasChoices []string `json:"platformAliasChoices,omitempty"`
	// PlatformAlias is the erun-type cloud alias actually resolved (or that a
	// not-signed-in/not-enrolled/no-permission state must act on).
	PlatformAlias string `json:"platformAlias,omitempty"`
	// PlatformURL is the platform base URL actually resolved (or that would be
	// contacted once signed in), so the surface can name what it talked to.
	PlatformURL string `json:"platformUrl,omitempty"`
	// PlatformIssuer/PlatformSubject prefill an enrollment request from the
	// identity already in hand, once a bearer minted successfully — set even
	// when the subsequent identity read itself failed (not-enrolled).
	PlatformIssuer  string `json:"platformIssuer,omitempty"`
	PlatformSubject string `json:"platformSubject,omitempty"`
	// AccessRemedies keys each panel's restricted route to the copyable grant
	// that would give the caller the access they were refused, so a restricted
	// tab hands over the request instead of only naming what is missing.
	// Absent when no role covers the access or the roles could not be read.
	AccessRemedies map[string]platformclient.PlatformAccessRemedy `json:"accessRemedies,omitempty"`
	User           *uiTenantDashboardUser                         `json:"user,omitempty"`
	// Users is the Users tab's roster: the tenant's users, read from
	// GET /v1/users. Distinct from User above, which is the caller's own
	// identity from whoami — the tab lists the tenant, not the caller, and an
	// unreadable roster is reported on the panel rather than collapsing back
	// to that one row.
	Users      []uiTenantDashboardUser   `json:"users,omitempty"`
	Reviews    []uiTenantDashboardReview `json:"reviews,omitempty"`
	MergeQueue []uiTenantDashboardReview `json:"mergeQueue,omitempty"`
	Builds     []uiTenantDashboardBuild  `json:"builds,omitempty"`
	// Pipeline is the Pipeline tab's own view: every job and review the
	// platform holds for this tenant, unioned on the issue each belongs to
	// and labelled by the rung it stands on. It spans the Reviews tab rather
	// than repeating it — planned work with no branch and no review is only
	// visible here.
	Pipeline    []uiPipelineIssue        `json:"pipeline,omitempty"`
	AuditEvents []uiTenantDashboardAudit `json:"auditEvents,omitempty"`
	Panels      []uiTenantDashboardPanel `json:"panels,omitempty"`
	// CanCreateReview and CanAdvanceMergeQueue report whether the signed-in user
	// may attempt those writes at all, so the composing actions can be hidden
	// rather than rendered to fail on submit — the same contract CanComment
	// already gives the reply composer.
	CanCreateReview      bool `json:"canCreateReview"`
	CanAdvanceMergeQueue bool `json:"canAdvanceMergeQueue"`
	// CanOverrideMergeQueue reports whether the signed-in user may bypass the
	// unresolved-thread gate, a distinct (usually narrower) grant from
	// CanAdvanceMergeQueue's — see tenantDashboardWriteOverrideAdvanceMergeQueue.
	CanOverrideMergeQueue bool `json:"canOverrideMergeQueue"`
	// MineReviewCount/WaitingOnMeReviewCount are the Reviews tab's filter
	// buttons' own discovery signal: how many reviews match each filter,
	// visible before the caller clicks either one. Unset (rather than 0) when
	// the caller cannot read reviews at all, or has no signed-in user id to
	// filter by.
	MineReviewCount        *int `json:"mineReviewCount,omitempty"`
	WaitingOnMeReviewCount *int `json:"waitingOnMeReviewCount,omitempty"`
	// Contexts/Environments are the Registration tab's two lists: the
	// platform's own cloud contexts (managed clusters) and hosted
	// environments — the objects `erun platform` registers, distinct from
	// (and with no automatic link to) this machine's local tenant/env config.
	// Each degrades independently (ContextsRestricted/ContextsError,
	// EnvironmentsRestricted/EnvironmentsError) so a caller denied one list,
	// or a list read that fails, does not blank the other.
	Contexts               []uiPlatformContext     `json:"contexts,omitempty"`
	ContextsRestricted     string                  `json:"contextsRestricted,omitempty"`
	ContextsError          string                  `json:"contextsError,omitempty"`
	Environments           []uiPlatformEnvironment `json:"environments,omitempty"`
	EnvironmentsRestricted string                  `json:"environmentsRestricted,omitempty"`
	EnvironmentsError      string                  `json:"environmentsError,omitempty"`
	// CanCreateContext/CanRegisterEnvironment/CanDeployEnvironment/
	// CanStopEnvironment/CanDeleteEnvironment report whether the signed-in
	// user may attempt each registration write at all, so the composing
	// forms/buttons can be hidden rather than rendered to fail on submit,
	// mirroring CanCreateReview/CanAdvanceMergeQueue above. Previewing an
	// environment (PreviewPlatformEnvironment) shares CanRegisterEnvironment's
	// own gate — it is the same POST /v1/environments route, requested with
	// preview:true — rather than a separate capability.
	CanCreateContext       bool `json:"canCreateContext"`
	CanRegisterEnvironment bool `json:"canRegisterEnvironment"`
	CanDeployEnvironment   bool `json:"canDeployEnvironment"`
	CanStopEnvironment     bool `json:"canStopEnvironment"`
	CanDeleteEnvironment   bool `json:"canDeleteEnvironment"`
	// MyInviteRequest is the caller's own most recent invite request, set
	// whenever a bearer minted successfully (PlatformIssuer/PlatformSubject is
	// set) regardless of whether the identity read itself succeeded — a
	// not-enrolled caller is exactly who needs to see this. nil means either
	// never submitted one, or the read itself failed — MyInviteRequestError
	// distinguishes the two; a nil MyInviteRequest with no
	// MyInviteRequestError is the genuine "never submitted" case.
	MyInviteRequest *uiInviteRequest `json:"myInviteRequest,omitempty"`
	// MyInviteRequestError is set when the platform round trip for
	// MyInviteRequest failed for a reason other than "none exists" (a
	// transport fault, a 5xx) — it must never be presented as "never
	// requested", since the caller could already have a pending or approved
	// request the dashboard just failed to read.
	MyInviteRequestError string `json:"myInviteRequestError,omitempty"`
	// InviteRequests/PendingInviteRequestCount are the operator/admin queue:
	// every pending request naming this tenant (or, for an operations-scoped
	// caller, every tenant). PendingInviteRequestCount is unset (rather than
	// 0) when the caller cannot read the queue at all, mirroring
	// MineReviewCount's degrade-by-permission contract above.
	InviteRequests            []uiInviteRequest `json:"inviteRequests,omitempty"`
	PendingInviteRequestCount *int              `json:"pendingInviteRequestCount,omitempty"`
	CanApproveInviteRequests  bool              `json:"canApproveInviteRequests"`
	CanDeclineInviteRequests  bool              `json:"canDeclineInviteRequests"`
	// InviteRequestRateLimitWindowSeconds is the platform's current
	// per-identity invite-request submission window, best effort (0 when it
	// could not be read) — the request dialog uses it to show "you can submit
	// once every N seconds" before a first attempt, not only after a 429.
	InviteRequestRateLimitWindowSeconds int `json:"inviteRequestRateLimitWindowSeconds,omitempty"`
}

// uiPlatformContext mirrors eruncommon.PlatformContext's JSON-safe subset the
// dashboard's Registration tab renders.
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

// uiPlatformEnvironment mirrors eruncommon.PlatformEnvironment's JSON-safe
// subset the dashboard's Registration tab renders.
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

// uiCreatePlatformContextInput registers (or, with Preview set, only
// previews) a cloud context on the platform: the hosted managed cluster an
// environment deploys into.
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

// uiPlatformContextOutcome is CreatePlatformContext's result. Kind
// "conflict"/"unavailable" is a refusal that is itself an expected,
// actionable state, carried in Message verbatim from the platform — never
// rendered as a raw error; only "accepted" carries Context (a real create) or
// Plan (a preview).
type uiPlatformContextOutcome struct {
	Kind    string             `json:"kind"`
	Context *uiPlatformContext `json:"context,omitempty"`
	Plan    []string           `json:"plan,omitempty"`
	Message string             `json:"message,omitempty"`
}

// uiPlatformProvisionResult mirrors eruncommon.PlatformProvisionResult:
// always a successful preview, QuotaOk names whether the plan can actually
// register without hitting the tenant's quota cap.
type uiPlatformProvisionResult struct {
	Plan    []string `json:"plan"`
	QuotaOk bool     `json:"quotaOk"`
}

// uiRegisterPlatformEnvironmentInput registers a hosted environment, or —
// passed to PreviewPlatformEnvironment instead of RegisterPlatformEnvironment
// — resolves the ordered plan the exact same fields would submit without
// creating anything: one field set backs both actions, so the plan an
// operator previews can never diverge from what register then does. Adopt
// records an environment that already exists — see
// eruncommon.PlatformCreateEnvironmentParams.Adopt — instead of asking the
// platform to provision one.
type uiRegisterPlatformEnvironmentInput struct {
	Tenant            string `json:"tenant"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	ContextID         string `json:"contextId,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	Adopt             bool   `json:"adopt,omitempty"`
}

// uiPlatformEnvironmentActionInput is Deploy/Stop/Delete's shared input: the
// registered environment to act on, plus an optional Version (deploy only)
// for redeploying at an explicit published version.
type uiPlatformEnvironmentActionInput struct {
	Tenant        string `json:"tenant"`
	EnvironmentID string `json:"environmentId"`
	Version       string `json:"version,omitempty"`
}

// uiPlatformEnvironmentOutcome is the shared result for
// RegisterPlatformEnvironment/DeployPlatformEnvironment/
// StopPlatformEnvironment/DeletePlatformEnvironment. Kind "conflict" (a
// quota cap, or another deploy/delete already in flight) and "unavailable"
// (no deploy executor configured on this control plane) are expected,
// actionable outcomes carried in Message verbatim from the platform — never
// raw errors; only "accepted" carries Environment.
type uiPlatformEnvironmentOutcome struct {
	Kind        string                 `json:"kind"`
	Environment *uiPlatformEnvironment `json:"environment,omitempty"`
	Message     string                 `json:"message,omitempty"`
}

// uiTenantDashboardPanel is one panel's own outcome. It is what lets the tab
// strip tell "there is nothing here" apart from "you may not look": a panel the
// caller lacks the permission for carries Restricted and is not rendered, while
// one that failed carries Error and does not blank its neighbours.
type uiTenantDashboardPanel struct {
	Tab string `json:"tab"`
	// Restricted names the API read the caller lacks, in canonical form
	// ("GET /v1/audit-events"), so the reason can be shown rather than inferred.
	Restricted string `json:"restricted,omitempty"`
	Error      string `json:"error,omitempty"`
}

type uiTenantDashboardUser struct {
	TenantID string `json:"tenantId"`
	// TenantName is the name the platform itself gives the tenant these rows
	// belong to, from whoami. It is set on the caller's own row only, where
	// the identity read resolved it, and is the one value that names whose
	// reviews, queue, users and audit a dashboard is rendering when the local
	// tenant it was opened from is not the platform tenant its credential
	// reaches. Empty on a platform that reports no name.
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
	// AuthorUsername is the tenant user directory's display name for
	// AuthorUserID, resolved best-effort (see tenant_dashboard.go's
	// tenantDashboardUsernames). Empty when it could not be resolved, so the
	// frontend falls back to the raw id rather than showing nothing (#1378).
	AuthorUsername string `json:"authorUsername,omitempty"`
	Name           string `json:"name"`
	// Repository is the repository the review's branches belong to, as the
	// platform records it. Empty for a review created before the platform
	// recorded one; the merge-queue panel names it so a queue spanning more
	// than one repository is visibly not one queue.
	Repository   string `json:"repository,omitempty"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
	Status       string `json:"status"`
	// UnresolvedThreads is the review's "still being discussed" signal at a
	// glance, from the row rather than only inside the detail dialog. Left
	// unset (rather than 0) when it was not computed for this listing (see
	// tenant_dashboard.go's reviewThreadCounts) so a caller can tell "zero
	// unresolved" apart from "not read for this row".
	UnresolvedThreads *int `json:"unresolvedThreads,omitempty"`
	// Blocked is AdvanceMergeQueue's own report that it refused to promote
	// this review: the queue head still has unresolved comment threads
	// (UnresolvedThreads then carries the count). Every other read path
	// leaves it unset — it is not a property of the review itself, only of
	// that one call's outcome.
	Blocked           bool   `json:"blocked,omitempty"`
	LastFailedBuildID string `json:"lastFailedBuildId,omitempty"`
	LastReadyBuildID  string `json:"lastReadyBuildId,omitempty"`
	LastMergedBuildID string `json:"lastMergedBuildId,omitempty"`
	CreatedAt         string `json:"createdAt,omitempty"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
}

type uiTenantDashboardBuild struct {
	BuildID  string `json:"buildId"`
	TenantID string `json:"tenantId"`
	// ReviewID is empty for a build with no review attached (erun#1954) --
	// identified by EnvironmentID instead.
	ReviewID        string `json:"reviewId,omitempty"`
	ReviewName      string `json:"reviewName,omitempty"`
	EnvironmentID   string `json:"environmentId,omitempty"`
	EnvironmentName string `json:"environmentName,omitempty"`
	Successful      bool   `json:"successful"`
	CommitID        string `json:"commitId"`
	Version         string `json:"version"`
	// Profile is the bounded per-step profile this build self-reported, when
	// it collected one -- nil for a build reported before this feature
	// existed, or one whose caller collected none.
	Profile   *core.BuildProfileSummary `json:"profile,omitempty"`
	CreatedAt string                    `json:"createdAt,omitempty"`
	UpdatedAt string                    `json:"updatedAt,omitempty"`
}

type uiTenantDashboardAudit struct {
	Type      string `json:"type"`
	Actor     string `json:"actor,omitempty"`
	Action    string `json:"action"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// uiPipelineIssue is one issue's own group of pipeline work, with every item
// keyed on it. IssueKey is empty for the single group that names no issue,
// which the platform orders last -- the view shows work without an issue
// rather than guessing an issue for it.
type uiPipelineIssue struct {
	IssueKey string           `json:"issueKey"`
	Items    []uiPipelineItem `json:"items"`
}

// uiPipelineItem is one piece of work on one rung, with the record it came
// from. Exactly one of Job or Review is set: a row that could not say which
// it was would send an operator looking for a branch that does not exist (a
// job) or a job that does (a review).
//
// Rung is the platform's own label (PLANNED, IN_PROGRESS, REVIEW_OPEN,
// READY, MERGING, MERGED, or an outcome such as FAILED), never re-derived
// here: the desktop renders whatever the platform said, under the shared
// vocabulary in erun-kit's pipelineRungs.
type uiPipelineItem struct {
	IssueKey string `json:"issueKey"`
	// IssueRef/IssueRefSource are the item's own link and where it came from,
	// so a branch-derived one renders as inferred. Empty IssueRefSource is
	// "the platform stated no link", never "the author declared this".
	IssueRef       string            `json:"issueRef,omitempty"`
	IssueRefSource string            `json:"issueRefSource,omitempty"`
	Rung           string            `json:"rung"`
	Job            *uiPipelineJob    `json:"job,omitempty"`
	Review         *uiPipelineReview `json:"review,omitempty"`
}

// uiPipelineJob is the job half: work that exists as a record and carries its
// own issue reference and owning actor. It has no branch, and nothing derives
// one for it.
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

// uiPipelineReview is the review half: work proposed for merge, with the
// branches it moves between.
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

// uiReviewDetail is the Reviews tab's per-row detail: the review itself, its
// comment threads, its recorded builds, and its position in its target
// branch's merge queue. Each sub-read degrades independently — the same
// restricted-vs-failed-vs-empty distinction the dashboard's own panels make —
// so one forbidden or failing read never blanks the rest of the detail.
type uiReviewDetail struct {
	ReviewID string `json:"reviewId"`
	// APIError is a whole-detail failure: identity could not be read, so no
	// capability set exists to gate the reads below honestly.
	APIError   string `json:"apiError,omitempty"`
	Restricted string `json:"restricted,omitempty"`
	// AccessRemedies keys each restricted route above to the copyable grant
	// that would give the caller the access they were refused, so a denial
	// hands over the request instead of only naming what is missing. Absent
	// when no role covers the access or the roles could not be read.
	AccessRemedies     map[string]platformclient.PlatformAccessRemedy `json:"accessRemedies,omitempty"`
	Error              string                                         `json:"error,omitempty"`
	Review             *uiTenantDashboardReview                       `json:"review,omitempty"`
	Comments           []uiReviewComment                              `json:"comments,omitempty"`
	CommentsRestricted string                                         `json:"commentsRestricted,omitempty"`
	CommentsError      string                                         `json:"commentsError,omitempty"`
	Builds             []uiTenantDashboardBuild                       `json:"builds,omitempty"`
	BuildsRestricted   string                                         `json:"buildsRestricted,omitempty"`
	BuildsError        string                                         `json:"buildsError,omitempty"`
	// QueuePosition is 1-based; 0 means the review is not in its target
	// branch's merge queue right now.
	QueuePosition int `json:"queuePosition,omitempty"`
	// UnresolvedThreads counts root comments still OPEN, valid whenever
	// Comments loaded (CommentsRestricted and CommentsError both empty).
	UnresolvedThreads int `json:"unresolvedThreads,omitempty"`
	// CanComment reports whether the signed-in user may reply at all, so the
	// composer can be hidden rather than rendered to fail on submit.
	CanComment bool `json:"canComment"`
	// CanClose mirrors CanComment for the close action.
	CanClose bool `json:"canClose"`
	// CanResolveComments mirrors CanComment for the resolve/unresolve action,
	// gated on its own write route since resolving a thread and posting to it
	// are different permissions on the platform.
	CanResolveComments  bool         `json:"canResolveComments"`
	Reviewers           []uiReviewer `json:"reviewers,omitempty"`
	ReviewersRestricted string       `json:"reviewersRestricted,omitempty"`
	ReviewersError      string       `json:"reviewersError,omitempty"`
	// CanAssignReviewers mirrors CanComment for the Add reviewers action,
	// gated on its own write route (POST .../reviewers) since assigning a
	// reviewer is a distinct permission from every other review write.
	CanAssignReviewers bool `json:"canAssignReviewers"`
	// CanRemoveReviewers mirrors CanAssignReviewers for the Remove action —
	// DELETE is a distinct route from POST, gated separately.
	CanRemoveReviewers bool `json:"canRemoveReviewers"`
	// AvailableReviewers lists the tenant's enrolled users the Add reviewers
	// picker may choose from — populated only when CanAssignReviewers is set,
	// so a caller who cannot assign never pays for the extra read. A
	// constrained picker, not a free-text id, is the point: it is how a
	// cross-tenant userId becomes structurally impossible to submit rather
	// than merely refused after the fact.
	AvailableReviewers []uiReviewer `json:"availableReviewers,omitempty"`
}

// uiReviewer is one entry in a review's assigned-reviewers list.
// Username is resolved best-effort the same way uiTenantDashboardReview's
// AuthorUsername is, empty when it could not be resolved.
type uiReviewer struct {
	UserID   string `json:"userId"`
	Username string `json:"username,omitempty"`
}

type uiReviewComment struct {
	CommentID     string `json:"commentId"`
	CreatorUserID string `json:"creatorUserId,omitempty"`
	// CreatorUsername mirrors uiTenantDashboardReview.AuthorUsername: the
	// tenant user directory's display name for CreatorUserID, resolved
	// best-effort, empty when it could not be resolved.
	CreatorUsername string `json:"creatorUsername,omitempty"`
	Status          string `json:"status"`
	ParentCommentID string `json:"parentCommentId,omitempty"`
	CommitID        string `json:"commitId"`
	FilePath        string `json:"filePath"`
	Line            int    `json:"line"`
	Body            string `json:"body"`
	CreatedAt       string `json:"createdAt,omitempty"`
}

// uiUpdateReviewCommentStatusInput resolves or unresolves a comment thread.
// CommentID must be a thread's root; the frontend never offers the action on
// a reply, so there is no reply-rejection path to surface here.
type uiUpdateReviewCommentStatusInput struct {
	Tenant    string `json:"tenant"`
	ReviewID  string `json:"reviewId"`
	CommentID string `json:"commentId"`
}

// uiCreateReviewReplyInput replies to an existing comment thread. CommitID,
// FilePath, and Line are copied from the parent comment the frontend already
// holds — a reply must anchor to the same line as the thread it joins — so
// Body is the only field the operator actually authors.
type uiCreateReviewReplyInput struct {
	Tenant          string `json:"tenant"`
	ReviewID        string `json:"reviewId"`
	ParentCommentID string `json:"parentCommentId"`
	CommitID        string `json:"commitId"`
	FilePath        string `json:"filePath"`
	Line            int    `json:"line"`
	Body            string `json:"body"`
}

// uiCreateReviewInput opens a review on the platform. sourceBranch must
// already be pushed to the remote — see ExecPush — since the review
// references it by name. Repository is the repository those branches belong
// to, in any form git accepts; the platform canonicalizes it. Without one the
// review cannot be placed in any repository's merge queue, so it is worth
// naming whenever the caller knows which checkout the work came from.
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
	// Repository and TargetBranch name the queue to advance. Both are
	// required: advancing is a single-queue-head write, and the two together
	// are what name one queue — a target branch alone names one only in a
	// tenant that serves exactly one repository.
	Repository   string `json:"repository"`
	TargetBranch string `json:"targetBranch"`
}

type uiOverrideAdvanceMergeQueueInput struct {
	Tenant       string `json:"tenant"`
	Repository   string `json:"repository"`
	TargetBranch string `json:"targetBranch"`
	// Reason is required by the platform and is recorded in its audit trail
	// alongside the caller's identity.
	Reason string `json:"reason"`
}

// uiAddReviewerInput assigns userId as a reviewer on a review. userId must
// already be enrolled in the caller's own tenant.
type uiAddReviewerInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
	UserID   string `json:"userId"`
}

// uiRemoveReviewerInput unassigns userId from a review's reviewers.
type uiRemoveReviewerInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
	UserID   string `json:"userId"`
}

// uiCreateReviewCommentInput starts a new top-level thread anchored to a diff
// line, as opposed to uiCreateReviewReplyInput's reply-in-an-existing-thread.
// Every field but Body is the anchor the operator picked by clicking a line in
// the diff panel, not a value they typed.
type uiCreateReviewCommentInput struct {
	Tenant   string `json:"tenant"`
	ReviewID string `json:"reviewId"`
	CommitID string `json:"commitId"`
	FilePath string `json:"filePath"`
	Line     int    `json:"line"`
	Body     string `json:"body"`
}
