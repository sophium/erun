// The reviews list's THREADS column and the review detail dialog's own
// unresolved count describe the same review, so they must never report
// different numbers for it. They are produced by two different reads — the
// list row's dashboard enrichment and the dialog's comment load — which is
// exactly how a row can render "-" while the dialog over it says
// "1 unresolved". These tests pin the agreement on the shape that produced
// that mismatch: one review, one thread, still OPEN.
import { describe, expect, it } from 'vitest';

import type { UITenantDashboard } from '@/types';

import { setReviewUnresolvedThreads, tenantDashboardSlice } from './slices/tenantDashboardSlice';
import { defaultTenantDashboard, type TenantDashboardState } from './state';
import {
  reviewDetailUnresolvedThreads,
  reviewRowUnresolvedThreads,
  unresolvedThreadsCountLabel,
  unresolvedThreadsLabel,
} from './tenantDashboardPanels';

// oneUnresolvedThreadReview is the review as the dashboard list describes it:
// the Go read model's per-row summary count, alongside the same review's
// comment threads as the detail dialog loads them — one root comment, OPEN,
// plus a reply that must not be counted as a second thread.
const listRow = { reviewId: 'review-1', unresolvedThreads: 1 };
const detail = {
  reviewId: 'review-1',
  unresolvedThreads: 1,
  comments: [
    {
      commentId: 'comment-1',
      parentCommentId: undefined,
      status: 'OPEN',
      body: 'this needs a fix',
    },
    {
      commentId: 'comment-2',
      parentCommentId: 'comment-1',
      status: 'OPEN',
      body: 'agreed',
    },
  ],
};

describe('unresolved-thread count agrees across the reviews list and the detail dialog', () => {
  it('reports the same count from both surfaces for a review with exactly one unresolved thread', () => {
    const fromList = reviewRowUnresolvedThreads(listRow);
    const fromDetail = reviewDetailUnresolvedThreads(detail);

    expect(fromList).toBe(1);
    expect(fromDetail).toBe(1);
    expect(fromList).toBe(fromDetail);
  });

  it('does not render the list cell as a bare dash while the dialog reports a count', () => {
    const fromList = reviewRowUnresolvedThreads(listRow);
    const fromDetail = reviewDetailUnresolvedThreads(detail);

    if (fromDetail === undefined) {
      throw new Error('the dialog derives its count from the comments it loaded');
    }
    expect(unresolvedThreadsCountLabel(fromList)).toBe(unresolvedThreadsLabel(fromDetail));
    expect(unresolvedThreadsCountLabel(fromList)).toBe('1 unresolved');
    expect(unresolvedThreadsCountLabel(fromList)).not.toBe('-');
  });

  it('derives the detail count from the threads it renders rather than a drifting summary field', () => {
    // The dialog has the thread list, so a stale summary count on the same
    // payload must not win: the number shown is the number of threads shown,
    // including after a resolve updates the threads without the summary.
    const resolvedDetail = {
      reviewId: 'review-1',
      unresolvedThreads: 1,
      comments: [{ commentId: 'comment-1', parentCommentId: undefined, status: 'CLOSED' }],
    };

    expect(reviewDetailUnresolvedThreads(resolvedDetail)).toBe(0);
    expect(unresolvedThreadsLabel(0)).toBe('All resolved');
  });

  it('spells out an unknown count instead of showing a dash that reads as none', () => {
    expect(unresolvedThreadsCountLabel(reviewRowUnresolvedThreads({}))).toBe('Unknown');
    expect(unresolvedThreadsCountLabel(undefined)).not.toBe('-');
  });

  it('brings a row with no computed count to the dialog’s count when the detail load writes back', () => {
    // The exact defect: the dashboard row carries no count while the dialog
    // over it reports one. Loading the detail is what makes them agree, so
    // the reducer that carries that write-back is where this is pinned.
    const dashboard: UITenantDashboard = {
      reviews: [{ reviewId: 'review-1', name: 'Add widget' }],
    } as UITenantDashboard;
    const withRow = { ...defaultTenantDashboard(), data: dashboard } as TenantDashboardState;
    const stagedRow = dashboard.reviews?.[0] ?? {};
    expect(unresolvedThreadsCountLabel(reviewRowUnresolvedThreads(stagedRow))).toBe('Unknown');

    const detailCount = reviewDetailUnresolvedThreads(detail);
    if (detailCount === undefined) {
      throw new Error('the dialog derives its count from the comments it loaded');
    }
    const next = tenantDashboardSlice.reducer(
      withRow,
      setReviewUnresolvedThreads({ reviewId: 'review-1', unresolvedThreads: detailCount }),
    );

    const row = next.data?.reviews?.[0];
    expect(reviewRowUnresolvedThreads(row ?? {})).toBe(detailCount);
    expect(unresolvedThreadsCountLabel(reviewRowUnresolvedThreads(row ?? {}))).toBe('1 unresolved');
  });
});
