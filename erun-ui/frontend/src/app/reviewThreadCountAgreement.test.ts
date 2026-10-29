// The reviews list's THREADS column and the detail dialog's unresolved count
// come from two different reads but must agree.
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

// The dashboard row's summary count alongside the dialog's threads: one OPEN
// root plus a reply that must not count as a second thread.
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
    // The thread list wins over a stale summary count on the same payload.
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
