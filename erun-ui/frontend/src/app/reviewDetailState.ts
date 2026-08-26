// Reviews-tab and review-detail state, split out of state.ts because that
// file crossed eslint's 500-line max-lines cap. Nothing here changes shape;
// state.ts re-exports the whole module so every existing `from './state'`
// import keeps working.
import type { UIReviewDetail, UITenantDashboard } from '@/types';

export type TenantDashboardTab = 'users' | 'reviews' | 'queue' | 'builds' | 'audit' | 'api-log';

// ReviewFilterState backs the Reviews tab's one-click discovery filters.
// Both can be on at once (author=me AND reviewer=me is a valid, if narrow,
// combination the platform already supports).
export interface ReviewFilterState {
  mine: boolean;
  waitingOnMe: boolean;
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
}

export const defaultReviewFilter = (): ReviewFilterState => ({
  mine: false,
  waitingOnMe: false,
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
});
