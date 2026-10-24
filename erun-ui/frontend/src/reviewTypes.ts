import type { UIAccessRemedy, UITenantDashboardBuild, UITenantDashboardReview } from './types';

export interface UIReviewDetailInput {
  tenant: string;
  reviewId: string;
}

// UIReviewDetail is a review's detail; each sub-read degrades independently.
export interface UIReviewDetail {
  reviewId: string;
  // apiError: identity could not be read, so no capability set can gate the reads below.
  apiError?: string;
  restricted?: string;
  // Copyable grant per restricted route; absent when no role covers it.
  accessRemedies?: Record<string, UIAccessRemedy>;
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
  // Valid only when comments loaded.
  unresolvedThreads?: number;
  canComment: boolean;
  canClose: boolean;
  canResolveComments: boolean;
  reviewers?: UIReviewer[];
  reviewersRestricted?: string;
  reviewersError?: string;
  canAssignReviewers: boolean;
  canRemoveReviewers: boolean;
  // A constrained picker keeps cross-tenant user ids unreachable.
  availableReviewers?: UIReviewer[];
}

export interface UIReviewer {
  userId: string;
  username?: string;
}

export interface UIAddReviewerInput {
  tenant: string;
  reviewId: string;
  userId: string;
}

export interface UIRemoveReviewerInput {
  tenant: string;
  reviewId: string;
  userId: string;
}

export interface UIReviewComment {
  commentId: string;
  creatorUserId?: string;
  creatorUsername?: string;
  status: string;
  parentCommentId?: string;
  commitId: string;
  filePath: string;
  line: number;
  body: string;
  createdAt?: string;
}

// UICreateReviewReplyInput replies to a thread; the anchor is copied from the parent comment.
export interface UICreateReviewReplyInput {
  tenant: string;
  reviewId: string;
  parentCommentId: string;
  commitId: string;
  filePath: string;
  line: number;
  body: string;
}

// UICreateReviewInput opens a review; sourceBranch must already be pushed.
export interface UICreateReviewInput {
  tenant: string;
  // Any form git accepts. Empty means none recorded: a remote worktree's checkout
  // may be unreadable from this host.
  repository: string;
  name: string;
  targetBranch: string;
  sourceBranch: string;
}

export interface UICloseReviewInput {
  tenant: string;
  reviewId: string;
}

// UIUpdateReviewCommentStatusInput resolves or unresolves a thread; commentId is its root.
export interface UIUpdateReviewCommentStatusInput {
  tenant: string;
  reviewId: string;
  commentId: string;
}

export interface UIAdvanceMergeQueueInput {
  tenant: string;
  // Both are required: a target branch alone names one queue only in a single-repository tenant.
  repository: string;
  targetBranch: string;
}

// UIOverrideAdvanceMergeQueueInput bypasses the unresolved-thread gate; the reason is audited.
export interface UIOverrideAdvanceMergeQueueInput {
  tenant: string;
  repository: string;
  targetBranch: string;
  reason: string;
}

// UICreateReviewCommentInput starts a top-level thread anchored to a diff line.
export interface UICreateReviewCommentInput {
  tenant: string;
  reviewId: string;
  commitId: string;
  filePath: string;
  line: number;
  body: string;
}

// UIExecCommitInput commits the working tree; branch is verified server-side.
export interface UIExecCommitInput {
  branch: string;
  message: string;
}

export interface UIExecPushInput {
  branch: string;
  remote?: string;
}

// Mirrors eruncommon's CommitWorkingTreeResult/PushWorkingTreeBranchResult.
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

// UIReviewCreateCapability carries an optional copyable grant for a refused write.
export interface UIReviewCreateCapability {
  canCreate: boolean;
  restricted?: string;
  accessRemedy?: UIAccessRemedy;
}
