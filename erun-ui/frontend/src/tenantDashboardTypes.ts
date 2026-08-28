// Tenant dashboard read-model types, split out of types.ts to keep that file
// under eslint's 500-line max-lines cap (see reviewTypes.ts/diffTypes.ts for
// the same pattern). Nothing here changes shape; types.ts re-exports the
// whole module so every existing `from './types'` import keeps working.

export interface UITenantDashboardInput {
  tenant: string;
  environment?: string;
  mcpUrl?: string;
  kubernetesContext?: string;
  // platformAlias optionally names which configured erun-type cloud alias's
  // platform to read; empty defers to the caller's sole configured erun
  // alias (or reports every alias as a choice when more than one exists).
  platformAlias?: string;
  // reviewFilterMine/reviewFilterWaitingOnMe are the Reviews tab's one-click
  // discovery filters, resolved server-side to the signed-in user's own id.
  reviewFilterMine?: boolean;
  reviewFilterWaitingOnMe?: boolean;
}

// UITenantPlatformState names why the platform identity is not ready to load
// the dashboard, so the UI can render the one action that resolves it. Kept
// as plain string values (matching the Go side and every other status-like
// field in this file, e.g. UITenantDashboardPanel) rather than a literal
// union, since the value crosses the Wails boundary as an untyped string. ""
// means the platform resolved and the load proceeded.
export const TENANT_PLATFORM_STATE_NOT_CONNECTED = 'not-connected';
export const TENANT_PLATFORM_STATE_CHOOSE_ALIAS = 'choose-alias';
export const TENANT_PLATFORM_STATE_NOT_SIGNED_IN = 'not-signed-in';
export const TENANT_PLATFORM_STATE_NOT_ENROLLED = 'not-enrolled';
export const TENANT_PLATFORM_STATE_NO_PERMISSION = 'no-permission';

export interface UITenantDashboard {
  tenant: string;
  environment?: string;
  apiUrl?: string;
  // apiError is a whole-dashboard failure (the caller's identity could not be
  // read). A single panel's own failure lives on that panel.
  apiError?: string;
  apiLog?: string;
  apiLogError?: string;
  // platformState/platformAliasChoices/platformAlias/platformUrl/
  // platformIssuer/platformSubject describe why the platform identity is not
  // ready (or, once ready, what was actually resolved) — see the
  // TENANT_PLATFORM_STATE_* constants above.
  platformState?: string;
  platformAliasChoices?: string[];
  platformAlias?: string;
  platformUrl?: string;
  platformIssuer?: string;
  platformSubject?: string;
  user?: UITenantDashboardUser;
  reviews?: UITenantDashboardReview[];
  mergeQueue?: UITenantDashboardReview[];
  builds?: UITenantDashboardBuild[];
  auditEvents?: UITenantDashboardAudit[];
  panels?: UITenantDashboardPanel[];
  // canCreateReview and canAdvanceMergeQueue report whether the signed-in user
  // may attempt those writes at all, so the composing actions can be hidden
  // rather than rendered to fail on submit.
  canCreateReview: boolean;
  canAdvanceMergeQueue: boolean;
  // canOverrideMergeQueue is a distinct (usually narrower) grant from
  // canAdvanceMergeQueue's: it gates the unresolved-thread gate's bypass,
  // authorized on its own platform route.
  canOverrideMergeQueue: boolean;
  // mineReviewCount/waitingOnMeReviewCount are the Reviews tab's Mine /
  // Waiting-on-me filter buttons' own discovery signal — how many reviews
  // match each, visible before either is clicked. Undefined when the caller
  // cannot read reviews, or has no signed-in user id to filter by.
  mineReviewCount?: number;
  waitingOnMeReviewCount?: number;
  // contexts/environments are the Registration tab's two lists: the
  // platform's own cloud contexts (managed clusters) and hosted
  // environments — objects distinct from (and with no automatic link to)
  // this machine's local tenant/env config. Each degrades independently:
  // a caller denied one list, or whose read of one failed, still sees the
  // other (contextsRestricted/contextsError, environmentsRestricted/
  // environmentsError).
  contexts?: UIPlatformContext[];
  contextsRestricted?: string;
  contextsError?: string;
  environments?: UIPlatformEnvironment[];
  environmentsRestricted?: string;
  environmentsError?: string;
  // canCreateContext/canRegisterEnvironment/canPreviewProvision/
  // canDeployEnvironment/canStopEnvironment/canDeleteEnvironment mirror
  // canCreateReview/canAdvanceMergeQueue above for the Registration tab's
  // own writes.
  canCreateContext: boolean;
  canRegisterEnvironment: boolean;
  canPreviewProvision: boolean;
  canDeployEnvironment: boolean;
  canStopEnvironment: boolean;
  canDeleteEnvironment: boolean;
}

// UIPlatformContext mirrors a hosted cloud context (managed cluster) the
// Registration tab lists and can create.
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

// UIPlatformEnvironment mirrors a hosted environment the Registration tab
// lists, registers, and deploys/stops/deletes.
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
  // deleteError names why a delete attempt did not tear the namespace down,
  // set only when status is "deletion-blocked".
  deleteError?: string;
}

// UICreatePlatformContextInput registers (or, with preview set, only
// previews) a cloud context, mirroring `erun platform context create
// [--preview]`.
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

// UIPlatformContextOutcome is CreatePlatformContext's result. kind
// "conflict"/"unavailable" is an expected, actionable refusal carried in
// `message` verbatim from the platform — render it as a recoverable state,
// never a raw error. Only "accepted" carries `context` (a real create) or
// `plan` (a preview).
// kind is plain string (not a literal union), matching every other
// status-like field crossing the Wails boundary in this file — the Go side
// carries "accepted" | "conflict" | "unavailable" as an untyped string.
export interface UIPlatformContextOutcome {
  kind: string;
  context?: UIPlatformContext;
  plan?: string[];
  message?: string;
}

// UIPlatformProvisionInput previews the ordered plan for provisioning a
// hosted environment without creating anything, mirroring `erun platform
// provision`. Pass either kubernetesContext to reuse an existing cluster, or
// the context* trio to preview bootstrapping a new one.
export interface UIPlatformProvisionInput {
  tenant: string;
  envName: string;
  envType: string;
  kubernetesContext?: string;
  contextName?: string;
  contextCloudProviderAlias?: string;
  contextRegion?: string;
  contextInstanceType?: string;
  contextDiskType?: string;
  contextDiskSizeGb?: number;
}

// UIPlatformProvisionResult is always a successful preview; quotaOk names
// whether the plan can actually register without hitting the tenant's cap.
export interface UIPlatformProvisionResult {
  plan: string[];
  quotaOk: boolean;
}

// UIRegisterPlatformEnvironmentInput registers a hosted environment,
// mirroring `erun platform env register`.
export interface UIRegisterPlatformEnvironmentInput {
  tenant: string;
  name: string;
  type: string;
  contextId?: string;
  kubernetesContext?: string;
  runtimeVersion?: string;
}

// UIPlatformEnvironmentActionInput is Deploy/Stop/Delete's shared input.
export interface UIPlatformEnvironmentActionInput {
  tenant: string;
  environmentId: string;
  version?: string;
}

// UIPlatformEnvironmentOutcome is the shared result for
// RegisterPlatformEnvironment/DeployPlatformEnvironment/
// StopPlatformEnvironment/DeletePlatformEnvironment. kind "conflict" (a
// quota cap, or another deploy/delete already in flight) and "unavailable"
// (no deploy executor configured) are expected, actionable outcomes carried
// in `message` verbatim from the platform — never raw errors; only
// "accepted" carries `environment`.
export interface UIPlatformEnvironmentOutcome {
  kind: string;
  environment?: UIPlatformEnvironment;
  message?: string;
}

// UITenantDashboardPanel is one panel's own outcome: `restricted` names the API
// read the signed-in user lacks, so a panel they may not see is never rendered
// as an empty one.
export interface UITenantDashboardPanel {
  tab: string;
  restricted?: string;
  error?: string;
}

export interface UITenantDashboardUser {
  tenantId: string;
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
  // authorUsername is the tenant user directory's display name for
  // authorUserId, resolved best-effort. Undefined when it could not be
  // resolved, so the caller falls back to the raw id.
  authorUsername?: string;
  name: string;
  targetBranch: string;
  sourceBranch: string;
  status: string;
  // unresolvedThreads is undefined when it was not computed for this row
  // (e.g. the caller cannot read comments) — distinct from 0, which means
  // every thread is resolved.
  unresolvedThreads?: number;
  // blocked is AdvanceMergeQueue's own report that it refused to promote
  // this review (unresolvedThreads then carries the count). Every other read
  // path leaves it undefined — it describes that one call's outcome, not a
  // property of the review itself.
  blocked?: boolean;
  lastFailedBuildId?: string;
  lastReadyBuildId?: string;
  lastMergedBuildId?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface UITenantDashboardBuild {
  buildId: string;
  tenantId: string;
  reviewId: string;
  reviewName?: string;
  successful: boolean;
  commitId: string;
  version: string;
  createdAt?: string;
  updatedAt?: string;
}

// UIConnectERunPlatformInput is the "Connect to erunpaas.com" action's input:
// just the API base URL, discovered against the platform itself the same way
// `erun cloud init erun` already does.
export interface UIConnectERunPlatformInput {
  apiUrl: string;
}

// UIPlatformUserEnrollInput is the "not enrolled" state's enrollment attempt.
// Every field is prefilled from the identity already in hand
// (UITenantDashboard.platformIssuer/platformSubject), so the operator never
// retypes a value erun already knows.
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

export interface UITenantDashboardAudit {
  type: string;
  actor?: string;
  action: string;
  createdAt?: string;
}
