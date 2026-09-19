// The Reviews tab's status filter: what it opens on, what it shows, and what an
// empty selection means. These pin the two things the operator asked for — a
// status filter at all, and OPEN+MERGE as its default — plus the empty-selection
// rule, which is the one that could quietly read as "this tenant has no
// reviews" if it were ever changed to "show nothing".
import assert from 'node:assert/strict';

import { test } from 'vitest';

import {
  defaultReviewFilter,
  defaultReviewStatuses,
  reviewCountLabel,
  reviewsMatchingStatuses,
  reviewStatusCounts,
  reviewStatusFilterIsDefault,
  toggleReviewStatus,
} from './reviewDetailState';

const review = (status: string) => ({ status });

test('the Reviews tab opens on the statuses that still need someone', () => {
  assert.deepEqual(defaultReviewStatuses(), ['OPEN', 'MERGE']);
  assert.deepEqual(defaultReviewFilter().statuses, ['OPEN', 'MERGE']);
});

test('the untouched status set reads as default, and a narrowed one does not', () => {
  assert.equal(reviewStatusFilterIsDefault(['OPEN', 'MERGE']), true);
  // Same members, different order: still the default set.
  assert.equal(reviewStatusFilterIsDefault(['MERGE', 'OPEN']), true);
  assert.equal(reviewStatusFilterIsDefault(['OPEN']), false);
  assert.equal(reviewStatusFilterIsDefault(['OPEN', 'MERGE', 'MERGED']), false);
  assert.equal(reviewStatusFilterIsDefault([]), false);
});

test('reviewsMatchingStatuses keeps only the chosen statuses', () => {
  const reviews = [review('OPEN'), review('MERGED'), review('MERGE'), review('CLOSED')];
  assert.deepEqual(
    reviewsMatchingStatuses(reviews, ['OPEN', 'MERGE']).map((r) => r.status),
    ['OPEN', 'MERGE'],
  );
});

test('status matching ignores case and surrounding space', () => {
  const reviews = [review('open'), review(' Merge '), review('MERGED')];
  assert.deepEqual(
    reviewsMatchingStatuses(reviews, ['OPEN', 'MERGE']).map((r) => r.status),
    ['open', ' Merge '],
  );
});

test('an empty selection shows everything rather than nothing', () => {
  const reviews = [review('OPEN'), review('MERGED')];
  assert.equal(reviewsMatchingStatuses(reviews, []).length, 2);
});

test('toggleReviewStatus adds and removes, keeping the canonical order', () => {
  assert.deepEqual(toggleReviewStatus(['OPEN'], 'MERGED'), ['OPEN', 'MERGED']);
  assert.deepEqual(toggleReviewStatus(['OPEN', 'MERGE'], 'OPEN'), ['MERGE']);
  // Adding out of order still renders in the control's own order, so a chip
  // never moves under the operator's cursor.
  assert.deepEqual(toggleReviewStatus(['CLOSED'], 'OPEN'), ['OPEN', 'CLOSED']);
});

test('reviewStatusCounts reports every status, including the empty ones', () => {
  const counts = reviewStatusCounts([review('OPEN'), review('MERGED'), review('MERGED')]);
  assert.equal(counts.OPEN, 1);
  assert.equal(counts.MERGED, 2);
  // A status with no reviews still reports zero: a chip that appears and
  // disappears as history changes is harder to aim at than one reading 0.
  assert.equal(counts.FAILED, 0);
  assert.equal(counts.CLOSED, 0);
});

test('reviewCountLabel names both numbers only when rows are hidden', () => {
  assert.equal(reviewCountLabel(1, 1), '1 review');
  assert.equal(reviewCountLabel(0, 0), '0 reviews');
  assert.equal(reviewCountLabel(3, 155), '3 of 155 reviews');
  assert.equal(reviewCountLabel(1, 155), '1 of 155 reviews');
});
