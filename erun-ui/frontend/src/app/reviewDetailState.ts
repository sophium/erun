import {
  INVITE_REQUEST_KIND_JOIN_TENANT,
  type UIReviewDetail,
  type UITenantDashboard,
} from '@/types';

import { defaultRegistrationState, type RegistrationState } from './tenantRegistrationState';

export type TenantDashboardTab =
  | 'users'
  | 'reviews'
  | 'queue'
  | 'pipeline'
  | 'builds'
  | 'audit'
  | 'registration'
  | 'requests'
  | 'api-log';

// statuses is applied locally: GET /v1/reviews accepts only one status per query.
export interface ReviewFilterState {
  mine: boolean;
  waitingOnMe: boolean;
  statuses: string[];
}

export const reviewStatuses = ['OPEN', 'READY', 'MERGE', 'FAILED', 'MERGED', 'CLOSED'] as const;

// defaultReviewStatuses hides only MERGED and CLOSED, which otherwise bury the
// live reviews.
export const defaultReviewStatuses = (): string[] => ['OPEN', 'READY', 'MERGE', 'FAILED'];

export function reviewStatusFilterIsDefault(statuses: string[]): boolean {
  const fallback = defaultReviewStatuses();
  if (statuses.length !== fallback.length) {
    return false;
  }
  return fallback.every((status) => statuses.includes(status));
}

// reviewsMatchingStatuses shows everything for an empty selection, never nothing.
export function reviewsMatchingStatuses<T extends { status: string }>(
  reviews: T[],
  statuses: string[],
): T[] {
  if (statuses.length === 0) {
    return reviews;
  }
  const wanted = new Set(statuses.map((status) => status.trim().toUpperCase()));
  return reviews.filter((review) => wanted.has(review.status.trim().toUpperCase()));
}

// toggleReviewStatus keeps canonical order so chips never move under the cursor.
export function toggleReviewStatus(statuses: string[], status: string): string[] {
  const target = status.trim().toUpperCase();
  const next = statuses.includes(target)
    ? statuses.filter((candidate) => candidate !== target)
    : [...statuses, target];
  return reviewStatuses.filter((candidate) => next.includes(candidate));
}

// ReviewsFilterNarrowing separates the axes so the empty state can name the filter
// that is on. statuses omits the empty and default selections.
export interface ReviewsFilterNarrowing {
  authorship: '' | 'Mine' | 'Waiting on me' | 'both Mine and Waiting on me';
  statuses: string[];
}

export function reviewsFilterNarrowing(filter: ReviewFilterState): ReviewsFilterNarrowing {
  return {
    authorship: authorshipFilterPhrase(filter),
    statuses: reviewStatusFilterIsDefault(filter.statuses) ? [] : filter.statuses,
  };
}

function authorshipFilterPhrase(filter: ReviewFilterState): ReviewsFilterNarrowing['authorship'] {
  if (filter.mine && filter.waitingOnMe) {
    return 'both Mine and Waiting on me';
  }
  if (filter.mine) {
    return 'Mine';
  }
  return filter.waitingOnMe ? 'Waiting on me' : '';
}

// reviewsFilterIsNarrowing: an empty status selection is not a filter.
export function reviewsFilterIsNarrowing(filter: ReviewFilterState): boolean {
  const narrowing = reviewsFilterNarrowing(filter);
  return narrowing.authorship !== '' || narrowing.statuses.length > 0;
}

export function reviewsFilteredEmptyBody(filter: ReviewFilterState): string {
  const { authorship, statuses } = reviewsFilterNarrowing(filter);
  if (authorship !== '' && statuses.length > 0) {
    return `Nothing is ${authorship} right now, with status ${statuses.join(' or ')}.`;
  }
  if (authorship !== '') {
    return `Nothing is ${authorship} right now.`;
  }
  return `Nothing has status ${statuses.join(' or ')} right now.`;
}

export function reviewCountLabel(visible: number, total: number): string {
  if (visible === total) {
    return `${String(visible)} ${visible === 1 ? 'review' : 'reviews'}`;
  }
  // The noun agrees with the total: "1 of 155 reviews".
  return `${String(visible)} of ${String(total)} ${total === 1 ? 'review' : 'reviews'}`;
}

// reviewStatusCounts reports 0 rather than omitting a status, so chips stay put.
export function reviewStatusCounts(reviews: { status: string }[]): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const status of reviewStatuses) {
    counts[status] = 0;
  }
  for (const review of reviews) {
    const status = review.status.trim().toUpperCase();
    counts[status] = (counts[status] ?? 0) + 1;
  }
  return counts;
}

export interface TenantDashboardState {
  tenant: string;
  tab: TenantDashboardTab;
  loading: boolean;
  error: string;
  data: UITenantDashboard | null;
  reviewFilter: ReviewFilterState;
  // platformAliasOverride: empty defers to the server's sole-alias resolution.
  platformAliasOverride: string;
  // In the slice so switching tabs mid-edit keeps the draft.
  connectApiUrlDraft: string;
  connecting: boolean;
  connectError: string;
  enrollUsernameDraft: string;
  enrolling: boolean;
  enrollError: string;
  registration: RegistrationState;
  // requestRateLimitedUntil is an epoch-ms deadline, 0 when not rate-limited.
  requestDialogOpen: boolean;
  requestKindDraft: string;
  requestNoteDraft: string;
  requesting: boolean;
  requestError: string;
  requestRateLimitedUntil: number;
  // '' means no decline dialog is open.
  decliningInviteRequestId: string;
  declineReasonDraft: string;
  decidingInviteRequestId: string;
  decideInviteRequestError: string;
  issuedInviteLink: { inviteRequestId: string; token: string } | null;
}

// ReviewDetailState keeps draftBody across a failed submit.
export interface ReviewDetailState {
  open: boolean;
  reviewId: string;
  loading: boolean;
  error: string;
  data: UIReviewDetail | null;
  // Captured at load: the dialog can outlive the tenant dashboard as the diff
  // panel's commenting context.
  callerTenant: string;
  callerPlatformAlias: string;
  replyingTo: string;
  draftBody: string;
  submitting: boolean;
  submitError: string;
  closeConfirming: boolean;
  closing: boolean;
  closeError: string;
  resolvingCommentId: string;
  // Kept apart from resolvingCommentId so a failed resolve stays on its thread.
  resolveError: string;
  resolveErrorCommentId: string;
  // Null means no diff-line composer is open.
  newCommentAnchor: { commitId: string; filePath: string; line: number } | null;
  newCommentDraft: string;
  newCommentSubmitting: boolean;
  newCommentSubmitError: string;
  addReviewerOpen: boolean;
  addReviewerUserId: string;
  addReviewerSubmitting: boolean;
  addReviewerError: string;
  removeReviewerConfirmingUserId: string;
  removingReviewerId: string;
  removeReviewerError: string;
  removeReviewerErrorUserId: string;
}

export const defaultReviewFilter = (): ReviewFilterState => ({
  mine: false,
  waitingOnMe: false,
  statuses: defaultReviewStatuses(),
});

export const defaultTenantDashboard = (): TenantDashboardState => ({
  tenant: '',
  tab: 'users',
  loading: false,
  error: '',
  data: null,
  reviewFilter: defaultReviewFilter(),
  platformAliasOverride: '',
  connectApiUrlDraft: '',
  connecting: false,
  connectError: '',
  enrollUsernameDraft: '',
  enrolling: false,
  enrollError: '',
  registration: defaultRegistrationState(),
  requestDialogOpen: false,
  requestKindDraft: INVITE_REQUEST_KIND_JOIN_TENANT,
  requestNoteDraft: '',
  requesting: false,
  requestError: '',
  requestRateLimitedUntil: 0,
  decliningInviteRequestId: '',
  declineReasonDraft: '',
  decidingInviteRequestId: '',
  decideInviteRequestError: '',
  issuedInviteLink: null,
});

export const defaultReviewDetail = (): ReviewDetailState => ({
  open: false,
  reviewId: '',
  loading: false,
  error: '',
  data: null,
  callerTenant: '',
  callerPlatformAlias: '',
  replyingTo: '',
  draftBody: '',
  submitting: false,
  submitError: '',
  closeConfirming: false,
  closing: false,
  closeError: '',
  resolvingCommentId: '',
  resolveError: '',
  resolveErrorCommentId: '',
  newCommentAnchor: null,
  newCommentDraft: '',
  newCommentSubmitting: false,
  newCommentSubmitError: '',
  addReviewerOpen: false,
  addReviewerUserId: '',
  addReviewerSubmitting: false,
  addReviewerError: '',
  removeReviewerConfirmingUserId: '',
  removingReviewerId: '',
  removeReviewerError: '',
  removeReviewerErrorUserId: '',
});
