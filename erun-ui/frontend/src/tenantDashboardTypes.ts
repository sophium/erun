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
  // mineReviewCount/waitingOnMeReviewCount are the Reviews tab's Mine /
  // Waiting-on-me filter buttons' own discovery signal — how many reviews
  // match each, visible before either is clicked. Undefined when the caller
  // cannot read reviews, or has no signed-in user id to filter by.
  mineReviewCount?: number;
  waitingOnMeReviewCount?: number;
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
