// Reviews-tab and review-detail state, split out of state.ts because that
// file crossed eslint's 500-line max-lines cap. Nothing here changes shape;
// state.ts re-exports the whole module so every existing `from './state'`
// import keeps working.
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
  | 'gates'
  | 'pipeline'
  | 'builds'
  | 'audit'
  | 'registration'
  | 'requests'
  | 'api-log';

// ReviewFilterState backs the Reviews tab's filters.
//
// mine/waitingOnMe are the one-click discovery filters, and both can be on at
// once (author=me AND reviewer=me is a valid, if narrow, combination the
// platform already supports).
//
// statuses narrows the list by review status. It is applied locally rather than
// server-side, because the platform's GET /v1/reviews accepts a single `status`
// per query (PlatformReviewFilter.Status is one string) and cannot express
// "OPEN or MERGE" at all — while the dashboard already holds the tenant's whole
// review list, so narrowing it needs no round-trip.
export interface ReviewFilterState {
  mine: boolean;
  waitingOnMe: boolean;
  statuses: string[];
}

// reviewStatuses is the platform's own review-status vocabulary, in the order
// the filter presents it: every live status first, in lifecycle order, then the
// two terminal ones.
export const reviewStatuses = ['OPEN', 'READY', 'MERGE', 'FAILED', 'MERGED', 'CLOSED'] as const;

// defaultReviewStatuses is what the list shows with no filter chosen: every
// status that is not finished.
//
// A tenant accumulates MERGED and CLOSED reviews forever — one large tenant was
// at 155 reviews of which 68 were closed and 87 merged, so the unfiltered list
// was entirely finished work and the handful that still needed someone were
// buried in it. Opening on the live statuses makes the default view the one an
// operator actually wants, and the counted number honest about it.
//
// FAILED and READY belong in that set, not outside it. A failed build is the
// most actionable state a review can be in, and READY is a review waiting on
// its reviewers; excluding either would hide exactly the rows the filter exists
// to surface, and would leave the operator needing a filter just to see a
// broken build. MERGED and CLOSED are the only two that mean "nobody's
// problem any more", and they are the only two this hides.
export const defaultReviewStatuses = (): string[] => ['OPEN', 'READY', 'MERGE', 'FAILED'];

// reviewStatusFilterIsDefault reports whether statuses is the untouched
// default, so a panel can tell "narrowed by the operator" from "as it opens"
// without comparing literals at each call site.
export function reviewStatusFilterIsDefault(statuses: string[]): boolean {
  const fallback = defaultReviewStatuses();
  if (statuses.length !== fallback.length) {
    return false;
  }
  return fallback.every((status) => statuses.includes(status));
}

// reviewsMatchingStatuses narrows a loaded review list to the chosen statuses.
//
// An empty selection shows everything rather than nothing: turning every chip
// off is how an operator asks for the unfiltered list, and rendering an empty
// panel for it would read as "this tenant has no reviews" — the exact confusion
// the three-empty-states rule exists to prevent.
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

// toggleReviewStatus adds or removes one status, preserving the canonical
// order so the chip row never reshuffles under the operator's cursor.
export function toggleReviewStatus(statuses: string[], status: string): string[] {
  const target = status.trim().toUpperCase();
  const next = statuses.includes(target)
    ? statuses.filter((candidate) => candidate !== target)
    : [...statuses, target];
  return reviewStatuses.filter((candidate) => next.includes(candidate));
}

// ReviewsFilterNarrowing is the Reviews tab's filter state with the axes
// separated, so a consumer can name the filters that are on instead of
// describing all of them and hedging.
//
// authorship is the phrase for the authorship chips — the pair is kept whole,
// because "Mine and Waiting on me" is a different state from either one alone
// and collapsing it to a boolean is what made the empty state unable to say
// which it was looking at.
//
// statuses excludes the two selections that narrow nothing: an empty selection
// (how the unfiltered list is reached) and the opening default.
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

// reviewsFilterIsNarrowing reports whether anything is actually hiding rows,
// which is what decides between the tab's two empty states. Derived from the
// same axes the copy names, so the heading and the body can never disagree —
// and an empty status selection, which shows everything, is not a filter.
export function reviewsFilterIsNarrowing(filter: ReviewFilterState): boolean {
  const narrowing = reviewsFilterNarrowing(filter);
  return narrowing.authorship !== '' || narrowing.statuses.length > 0;
}

// reviewsFilteredEmptyBody names the filters that came up empty. The operator
// turned these on and got nothing; saying "the filters you've turned on" makes
// them re-read the toolbar to find out which one, and describing a conjunction
// when only one is active states something false about their own query.
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

// reviewCountLabel renders the list's own count, naming both numbers whenever
// a status filter is hiding rows. "3 of 155" is the honest reading; "3" alone
// would claim the tenant has three reviews.
export function reviewCountLabel(visible: number, total: number): string {
  if (visible === total) {
    return `${String(visible)} ${visible === 1 ? 'review' : 'reviews'}`;
  }
  // Both numbers shown: the noun belongs to the total. "1 of 155 reviews" is
  // the true sentence — the tenant has 155, one of which is on screen — where
  // agreeing with the visible count would read "1 of 155 review".
  return `${String(visible)} of ${String(total)} ${total === 1 ? 'review' : 'reviews'}`;
}

// reviewStatusCounts counts the loaded reviews per status, so each chip can
// carry the distribution it offers. A status with no reviews still reports 0
// rather than being omitted: "MERGED 0" is information, and a chip that appears
// and disappears as the tenant's history changes is harder to aim at.
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
  // platformAliasOverride is the operator's explicit pick when more than one
  // erun-type platform alias is configured (the choose-alias state); empty
  // defers to the server's own sole-alias resolution.
  platformAliasOverride: string;
  // connect*/enroll* back the not-connected/not-enrolled states' own inline
  // forms, kept here (rather than local component state) so switching tabs
  // or panels mid-edit does not lose an in-progress value (Nielsen #3).
  connectApiUrlDraft: string;
  connecting: boolean;
  connectError: string;
  enrollUsernameDraft: string;
  enrolling: boolean;
  enrollError: string;
  // registration backs the Registration tab's forms and per-environment
  // action state — split into its own module (tenantRegistrationState.ts)
  // since it is sizable on its own.
  registration: RegistrationState;
  // requestDialogOpen/requestKindDraft/requestNoteDraft/requesting/
  // requestError/requestRateLimitedUntil back the "Request an invitation"
  // dialog (NotEnrolledState's second action). requestRateLimitedUntil is an
  // epoch-ms deadline (0 when not rate-limited) so the dialog can render a
  // live countdown and keep submit disabled for the rest of the window
  // without a raw error — root AGENTS.md's "blocked, not broken" distinction.
  requestDialogOpen: boolean;
  requestKindDraft: string;
  requestNoteDraft: string;
  requesting: boolean;
  requestError: string;
  requestRateLimitedUntil: number;
  // decliningInviteRequestId/declineReasonDraft/decliningInviteRequest/
  // decideInviteRequestError back the Requests tab's per-row decline dialog
  // and the (non-dialog) issue-invitation action; '' means no row's decline
  // dialog is open.
  decliningInviteRequestId: string;
  declineReasonDraft: string;
  decidingInviteRequestId: string;
  decideInviteRequestError: string;
  // issuedInviteLink is the most recently minted invite link (tenant, token),
  // kept on screen after Issue invitation succeeds as the transferable
  // artefact/manual fallback the issue's onboarding flow calls for.
  issuedInviteLink: { inviteRequestId: string; token: string } | null;
}

// ReviewDetailState backs the dialog a Reviews-tab row opens. draftBody
// survives a failed reply submit (Nielsen #3, user control) — a submit error
// clears submitError but never the text the operator already typed.
export interface ReviewDetailState {
  open: boolean;
  reviewId: string;
  loading: boolean;
  error: string;
  data: UIReviewDetail | null;
  // callerTenant/callerPlatformAlias are the caller context resolved when the
  // review loaded, captured here rather than re-derived from
  // state.tenantDashboard on every write: closing this dialog keeps the
  // review as the diff panel's active commenting context (see
  // closeReviewDetail), and by then the operator may have navigated away
  // from the tenant dashboard entirely. callerPlatformAlias only backs the
  // "Log in" action a stale-identity write failure offers — the write itself
  // needs no alias, the platform resolves it server-side from callerTenant.
  callerTenant: string;
  callerPlatformAlias: string;
  replyingTo: string;
  draftBody: string;
  submitting: boolean;
  submitError: string;
  // closeConfirming is the inline "are you sure" step Close goes through
  // before the write fires — the same cancel-before-commitment boundary every
  // other side-effecting dashboard action gets.
  closeConfirming: boolean;
  closing: boolean;
  closeError: string;
  // resolvingCommentId is the thread root currently being resolved/unresolved
  // ('' when idle), so only that thread's action shows a busy state.
  resolvingCommentId: string;
  // resolveError/resolveErrorCommentId are kept apart from resolvingCommentId
  // (which clears once the request settles, success or failure) so a failed
  // resolve still shows its error against the right thread instead of no
  // thread at all.
  resolveError: string;
  resolveErrorCommentId: string;
  // newCommentAnchor is the diff line the operator clicked to start a new
  // top-level thread (as opposed to replyingTo, which continues an existing
  // one). Null means no diff-line composer is open.
  newCommentAnchor: { commitId: string; filePath: string; line: number } | null;
  newCommentDraft: string;
  newCommentSubmitting: boolean;
  newCommentSubmitError: string;
  // addReviewerOpen is the Add reviewers picker's own open/closed state —
  // not destructive, so unlike Close/Remove it needs no confirm step.
  addReviewerOpen: boolean;
  addReviewerUserId: string;
  addReviewerSubmitting: boolean;
  addReviewerError: string;
  // removeReviewerConfirmingUserId is the reviewer currently at the "are you
  // sure" step ('' when none) — Remove is access-revoking, so it gets the
  // same cancel-before-commitment boundary Close does.
  removeReviewerConfirmingUserId: string;
  // removingReviewerId is the reviewer currently being removed ('' when
  // idle), so only that row shows a busy state.
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
