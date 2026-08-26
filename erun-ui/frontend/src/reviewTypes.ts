// Review-detail types, split out of types.ts to keep that file under eslint's
// 500-line max-lines cap (see diffTypes.ts for the same pattern). Nothing here
// changes shape; types.ts re-exports the whole module so every existing
// `from './types'` import keeps working.
import type { UITenantDashboardBuild, UITenantDashboardReview } from './types';

export interface UIReviewDetailInput {
  tenant: string;
  apiUrl: string;
  cloudProviderAlias: string;
  reviewId: string;
}

// UIReviewDetail is the Reviews tab's per-row detail. Each sub-read degrades
// independently — the same restricted/error/empty distinction the dashboard's
// own panels make — so one forbidden or failing read never blanks the rest.
export interface UIReviewDetail {
  reviewId: string;
  // apiError is a whole-detail failure: identity could not be read, so no
  // capability set exists to gate the reads below honestly.
  apiError?: string;
  restricted?: string;
  error?: string;
  review?: UITenantDashboardReview;
  comments?: UIReviewComment[];
  commentsRestricted?: string;
  commentsError?: string;
  builds?: UITenantDashboardBuild[];
  buildsRestricted?: string;
  buildsError?: string;
  // queuePosition is 1-based; 0 means the review is not queued right now.
  queuePosition?: number;
  // unresolvedThreads counts root comments still OPEN; valid whenever
  // comments loaded (commentsRestricted and commentsError both unset).
  unresolvedThreads?: number;
  // canComment reports whether the signed-in user may reply at all, so the
  // composer can be hidden rather than rendered to fail on submit.
  canComment: boolean;
  // canClose mirrors canComment for the close action.
  canClose: boolean;
  // canResolveComments mirrors canComment for the resolve/unresolve action —
  // a distinct write route on the platform, gated separately.
  canResolveComments: boolean;
}

export interface UIReviewComment {
  commentId: string;
  creatorUserId?: string;
  // creatorUsername mirrors UITenantDashboardReview.authorUsername: the
  // tenant user directory's display name for creatorUserId, resolved
  // best-effort. Undefined when it could not be resolved.
  creatorUsername?: string;
  status: string;
  parentCommentId?: string;
  commitId: string;
  filePath: string;
  line: number;
  body: string;
  createdAt?: string;
}

// UICreateReviewReplyInput replies to an existing comment thread. commitId,
// filePath, and line are copied from the parent comment already held in
// state — a reply must anchor to the same line as the thread it joins — so
// body is the only field the operator actually authors.
export interface UICreateReviewReplyInput {
  tenant: string;
  apiUrl: string;
  cloudProviderAlias: string;
  reviewId: string;
  parentCommentId: string;
  commitId: string;
  filePath: string;
  line: number;
  body: string;
}

// UICreateReviewInput opens a review. sourceBranch must already be pushed to
// the remote (see UIExecPushInput) before the platform can reference it.
export interface UICreateReviewInput {
  tenant: string;
  apiUrl: string;
  cloudProviderAlias: string;
  name: string;
  targetBranch: string;
  sourceBranch: string;
}

export interface UICloseReviewInput {
  tenant: string;
  apiUrl: string;
  cloudProviderAlias: string;
  reviewId: string;
}

// UIUpdateReviewCommentStatusInput resolves or unresolves a comment thread.
// commentId must be a thread's root — the dialog only ever offers the action
// there, never on a reply.
export interface UIUpdateReviewCommentStatusInput {
  tenant: string;
  apiUrl: string;
  cloudProviderAlias: string;
  reviewId: string;
  commentId: string;
}

export interface UIAdvanceMergeQueueInput {
  tenant: string;
  apiUrl: string;
  cloudProviderAlias: string;
  targetBranch: string;
}

// UICreateReviewCommentInput starts a new top-level thread anchored to a diff
// line — the sibling of UICreateReviewReplyInput, which replies within an
// existing thread. Every field but body is the anchor the operator picked by
// clicking a line in the diff panel, not a value they typed.
export interface UICreateReviewCommentInput {
  tenant: string;
  apiUrl: string;
  cloudProviderAlias: string;
  reviewId: string;
  commitId: string;
  filePath: string;
  line: number;
  body: string;
}

// UIExecCommitInput commits every change in the selected environment's
// working tree. branch is the caller's belief about the current branch,
// verified server-side and refused loudly on mismatch.
export interface UIExecCommitInput {
  branch: string;
  message: string;
}

export interface UIExecPushInput {
  branch: string;
  remote?: string;
}

// UICommitWorkingTreeResult/UIPushWorkingTreeBranchResult mirror
// eruncommon.CommitWorkingTreeResult/PushWorkingTreeBranchResult — passed
// through the Wails boundary unwrapped, the same way diff results are.
export interface UICommitWorkingTreeResult {
  branch: string;
  commit: string;
  files: string[];
}

export interface UIPushWorkingTreeBranchResult {
  branch: string;
  remote: string;
  commit: string;
}
