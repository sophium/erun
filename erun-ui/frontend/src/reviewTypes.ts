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
  // canComment reports whether the signed-in user may reply at all, so the
  // composer can be hidden rather than rendered to fail on submit.
  canComment: boolean;
}

export interface UIReviewComment {
  commentId: string;
  creatorUserId?: string;
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
