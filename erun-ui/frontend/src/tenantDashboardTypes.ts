// UIAccessRemedy is the resolved grant command for a denied capability and the role it grants.
export interface UIAccessRemedy {
  command?: string;
  roleName?: string;
}

export interface UITenantDashboardInput {
  tenant: string;
  environment?: string;
  mcpUrl?: string;
  kubernetesContext?: string;
  // Empty defers to the sole erun alias, or offers every alias as a choice.
  platformAlias?: string;
  // Resolved server-side to the signed-in user's id.
  reviewFilterMine?: boolean;
  reviewFilterWaitingOnMe?: boolean;
}

// Platform-readiness states, plain strings across the Wails boundary; "" means
// the platform resolved.
export const TENANT_PLATFORM_STATE_NOT_CONNECTED = 'not-connected';
export const TENANT_PLATFORM_STATE_CHOOSE_ALIAS = 'choose-alias';
export const TENANT_PLATFORM_STATE_NOT_SIGNED_IN = 'not-signed-in';
export const TENANT_PLATFORM_STATE_NOT_ENROLLED = 'not-enrolled';
export const TENANT_PLATFORM_STATE_NO_PERMISSION = 'no-permission';
// The platform resolved the credential to a tenant other than the local one.
export const TENANT_PLATFORM_STATE_TENANT_MISMATCH = 'tenant-mismatch';

export interface UITenantDashboard {
  tenant: string;
  environment?: string;
  apiUrl?: string;
  // Whole-dashboard failure; panel failures live on the panel.
  apiError?: string;
  apiLog?: string;
  apiLogError?: string;
  platformState?: string;
  platformAliasChoices?: string[];
  platformAlias?: string;
  platformUrl?: string;
  platformIssuer?: string;
  platformSubject?: string;
  // Copyable grant per restricted route; absent when no role covers it.
  accessRemedies?: Record<string, UIAccessRemedy>;
  user?: UITenantDashboardUser;
  // The tenant roster, distinct from the caller's own user above.
  users?: UITenantDashboardUser[];
  reviews?: UITenantDashboardReview[];
  mergeQueue?: UITenantDashboardReview[];
  builds?: UITenantDashboardBuild[];
  // Spans jobs and reviews: planned work has no branch, so only this list shows it.
  pipeline?: UIPipelineIssue[];
  auditEvents?: UITenantDashboardAudit[];
  panels?: UITenantDashboardPanel[];
  canCreateReview: boolean;
  canAdvanceMergeQueue: boolean;
  // A separate, usually narrower grant: bypasses the unresolved-thread gate.
  canOverrideMergeQueue: boolean;
  // Undefined when the caller cannot read reviews or has no user id.
  mineReviewCount?: number;
  waitingOnMeReviewCount?: number;
  // Platform contexts and hosted environments, unlinked to local config; each
  // degrades independently.
  contexts?: UIPlatformContext[];
  contextsRestricted?: string;
  contextsError?: string;
  environments?: UIPlatformEnvironment[];
  environmentsRestricted?: string;
  environmentsError?: string;
  // Preview shares canRegisterEnvironment: it is the same POST /v1/environments route.
  canCreateContext: boolean;
  canRegisterEnvironment: boolean;
  canDeployEnvironment: boolean;
  canStopEnvironment: boolean;
  canDeleteEnvironment: boolean;
  // Set even when not enrolled; undefined is ambiguous without myInviteRequestError.
  myInviteRequest?: UIInviteRequest;
  // A failed read, never to be treated as "never requested".
  myInviteRequestError?: string;
  // pendingInviteRequestCount is undefined, not 0, when the queue is unreadable.
  inviteRequests?: UIInviteRequest[];
  pendingInviteRequestCount?: number;
  canApproveInviteRequests: boolean;
  canDeclineInviteRequests: boolean;
  // Best effort; 0 when unreadable.
  inviteRequestRateLimitWindowSeconds?: number;
}

// Per-tenant enrollment states for the sidebar icon, distinct from UITenantPlatformState.
export const TENANT_ENROLLMENT_LOCAL_ONLY = 'local-only';
export const TENANT_ENROLLMENT_PENDING = 'pending';
export const TENANT_ENROLLMENT_DECLINED = 'declined';
export const TENANT_ENROLLMENT_ENROLLED = 'enrolled';
// A round-trip failure, never "never requested".
export const TENANT_ENROLLMENT_UNKNOWN = 'unknown';
// Deliberately the same value as TENANT_PLATFORM_STATE_TENANT_MISMATCH.
export const TENANT_ENROLLMENT_TENANT_MISMATCH = TENANT_PLATFORM_STATE_TENANT_MISMATCH;

export interface UITenantPlatformEnrollmentStatus {
  tenant: string;
  state: string;
  declineReason?: string;
  // The platform the credential reached; absent when none answered.
  platformHost?: string;
  // Set only on TENANT_ENROLLMENT_TENANT_MISMATCH.
  platformTenant?: string;
}

export interface UIListTenantPlatformEnrollmentStatusesInput {
  tenants: string[];
}

// Mirror erun-common's PlatformInviteRequestKind*/PlatformInviteRequestStatus*.
export const INVITE_REQUEST_KIND_JOIN_TENANT = 'JOIN_TENANT';
export const INVITE_REQUEST_KIND_CREATE_TENANT = 'CREATE_TENANT';
export const INVITE_REQUEST_STATUS_PENDING = 'PENDING';
export const INVITE_REQUEST_STATUS_APPROVED = 'APPROVED';
export const INVITE_REQUEST_STATUS_DECLINED = 'DECLINED';

// UIInviteRequest mirrors the rendered subset of eruncommon.PlatformInviteRequest.
export interface UIInviteRequest {
  inviteRequestId: string;
  issuer: string;
  subject: string;
  email?: string;
  displayName?: string;
  kind: string;
  tenantName: string;
  environmentName?: string;
  note?: string;
  status: string;
  declineReason?: string;
  mintedInviteToken?: string;
  createdAt?: string;
  updatedAt?: string;
}

// UISubmitInviteRequestInput omits identity, which comes from the verified bearer.
export interface UISubmitInviteRequestInput {
  tenant: string;
  kind: string;
  tenantName: string;
  environmentName?: string;
  note?: string;
}

// UIInviteRequestRateLimited is a state to render, not a fault.
export interface UIInviteRequestRateLimited {
  retryAfterSeconds: number;
}

// UISubmitInviteRequestOutcome carries exactly one of request or rateLimited.
export interface UISubmitInviteRequestOutcome {
  request?: UIInviteRequest;
  rateLimited?: UIInviteRequestRateLimited;
}

export interface UITenantInput {
  tenant: string;
}

export interface UIDecideInviteRequestInput {
  tenant: string;
  inviteRequestId: string;
}

export interface UIDeclineInviteRequestInput {
  tenant: string;
  inviteRequestId: string;
  reason: string;
}

// UIPlatformContext mirrors a hosted cloud context (managed cluster).
export interface UIPlatformContext {
  contextId: string;
  name: string;
  provider: string;
  cloudProviderAlias?: string;
  region?: string;
  instanceType?: string;
  kubernetesContext?: string;
  status: string;
  provisionError?: string;
}

export interface UIPlatformEnvironment {
  environmentId: string;
  name: string;
  type: string;
  contextId?: string;
  kubernetesContext?: string;
  runtimeVersion?: string;
  status: string;
  provisionError?: string;
  deployedVersion?: string;
  // Set only when status is "deletion-blocked".
  deleteError?: string;
}

// UICreatePlatformContextInput mirrors `erun platform context create [--preview]`.
export interface UICreatePlatformContextInput {
  tenant: string;
  name: string;
  cloudProviderAlias: string;
  region: string;
  instanceType?: string;
  diskType?: string;
  diskSizeGb?: number;
  preview?: boolean;
}

// UIPlatformContextOutcome: "conflict"/"unavailable" are recoverable refusals
// with the platform's message; only "accepted" carries context or plan.
export interface UIPlatformContextOutcome {
  kind: string;
  context?: UIPlatformContext;
  plan?: string[];
  message?: string;
}

// UIPlatformProvisionResult is a preview; quotaOk says whether it fits the tenant's cap.
export interface UIPlatformProvisionResult {
  plan: string[];
  quotaOk: boolean;
}

// UIRegisterPlatformEnvironmentInput mirrors `erun platform env register`;
// preview uses the same fields. adopt requires kubernetesContext and forbids
// contextId/runtimeVersion.
export interface UIRegisterPlatformEnvironmentInput {
  tenant: string;
  name: string;
  type: string;
  contextId?: string;
  kubernetesContext?: string;
  runtimeVersion?: string;
  adopt?: boolean;
}

export interface UIPlatformEnvironmentActionInput {
  tenant: string;
  environmentId: string;
  version?: string;
}

// UIPlatformEnvironmentOutcome: "conflict"/"unavailable" are actionable outcomes
// with the platform's message; only "accepted" carries environment.
export interface UIPlatformEnvironmentOutcome {
  kind: string;
  environment?: UIPlatformEnvironment;
  message?: string;
}

// UITenantDashboardPanel: `restricted` names the read the user lacks.
export interface UITenantDashboardPanel {
  tab: string;
  restricted?: string;
  error?: string;
}

export interface UITenantDashboardUser {
  tenantId: string;
  // The platform's name for this tenant, set on the caller's own row only.
  tenantName?: string;
  userId: string;
  username?: string;
  roles?: string[];
  issuer?: string;
  subject?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface UITenantDashboardReview {
  reviewId: string;
  tenantId: string;
  authorUserId?: string;
  // Best effort; undefined falls back to the raw id.
  authorUsername?: string;
  name: string;
  // Undefined for reviews created before the platform recorded one.
  repository?: string;
  targetBranch: string;
  sourceBranch: string;
  status: string;
  // Undefined when not computed, distinct from 0.
  unresolvedThreads?: number;
  // Set only by AdvanceMergeQueue's refusal, not a property of the review.
  blocked?: boolean;
  lastFailedBuildId?: string;
  lastReadyBuildId?: string;
  lastMergedBuildId?: string;
  createdAt?: string;
  updatedAt?: string;
}

// UIBuildCgroupMetrics: when unavailable, render "not available", never zeros
// (which would read as "used no CPU").
export interface UIBuildCgroupMetrics {
  available: boolean;
  unavailable?: string;
  cpuSeconds?: number;
  cpuPercentOfQuota?: number;
  // The throttle ratio, not CPU percent alone, separates starved from well-sized steps.
  throttledPeriods?: number;
  totalPeriods?: number;
  throttledSeconds?: number;
  ioReadBytes?: number;
  ioWriteBytes?: number;
  peakMemoryBytes?: number;
}

// UIBuildProfileStep's name carries its ancestry path so same-named steps stay distinct.
export interface UIBuildProfileStep {
  name: string;
  durationSeconds: number;
  cgroup?: UIBuildCgroupMetrics;
}

// UIBuildProfileSummary is bounded to the top costliest steps; truncatedStepCount
// counts the rest.
export interface UIBuildProfileSummary {
  durationSeconds: number;
  failed?: boolean;
  cgroup?: UIBuildCgroupMetrics;
  topSteps?: UIBuildProfileStep[];
  totalStepCount?: number;
  truncatedStepCount?: number;
}

export interface UITenantDashboardBuild {
  buildId: string;
  tenantId: string;
  // Empty for a plain `erun build` self-report, identified by environmentId.
  reviewId?: string;
  reviewName?: string;
  environmentId?: string;
  environmentName?: string;
  successful: boolean;
  commitId: string;
  version: string;
  profile?: UIBuildProfileSummary;
  createdAt?: string;
  updatedAt?: string;
}

// UIConnectERunPlatformInput's tenant is omitted only by the settings dialog; a
// machine-global alias cannot move a tenant with its own alias selection.
export interface UIConnectERunPlatformInput {
  apiUrl: string;
  tenant?: string;
}

// UIPlatformUserEnrollInput is prefilled from the identity already resolved.
export interface UIPlatformUserEnrollInput {
  alias: string;
  username: string;
  issuer: string;
  subject: string;
}

export interface UIPlatformUser {
  userId: string;
  tenantId: string;
  username: string;
}

// UIPipelineItem has exactly one of job or review. rung is the platform's label,
// never re-derived here.
export interface UIPipelineItem {
  issueKey: string;
  // An INFERRED source was parsed from a branch name.
  issueRef?: string;
  issueRefSource?: string;
  rung: string;
  job?: UIPipelineJob;
  review?: UIPipelineReview;
}

// UIPipelineJob has no branch, and nothing derives one.
export interface UIPipelineJob {
  jobId: string;
  jobType: string;
  issueRef?: string;
  summary: string;
  status: string;
  actorKind: string;
  actorId: string;
  startedAt?: string;
  // Unset while the job is open.
  endedAt?: string;
}

export interface UIPipelineReview {
  reviewId: string;
  repository?: string;
  name: string;
  targetBranch: string;
  sourceBranch: string;
  status: string;
}

// UIPipelineIssue groups work by issue; empty issueKey is the last, unlinked group.
export interface UIPipelineIssue {
  issueKey: string;
  items: UIPipelineItem[];
}

export interface UITenantDashboardAudit {
  type: string;
  actor?: string;
  action: string;
  createdAt?: string;
}
