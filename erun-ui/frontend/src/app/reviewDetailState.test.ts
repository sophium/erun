// The Reviews tab's status filter: what it opens on, what it shows, and what an
// empty selection means. These pin what the operator asked for — a status filter
// at all, opening on the statuses that still need someone — plus the
// empty-selection rule, which is the one that could quietly read as "this tenant
// has no reviews" if it were ever changed to "show nothing".
import assert from 'node:assert/strict';

import { test } from 'vitest';

import {
  defaultReviewFilter,
  defaultReviewStatuses,
  reviewCountLabel,
  reviewsFilteredEmptyBody,
  reviewsFilterIsNarrowing,
  reviewsMatchingStatuses,
  reviewStatusCounts,
  reviewStatusFilterIsDefault,
  toggleReviewStatus,
} from './reviewDetailState';

const review = (status: string) => ({ status });

test('the Reviews tab opens on the statuses that still need someone', () => {
  assert.deepEqual(defaultReviewStatuses(), ['OPEN', 'READY', 'MERGE', 'FAILED']);
  assert.deepEqual(defaultReviewFilter().statuses, ['OPEN', 'READY', 'MERGE', 'FAILED']);
});

test('the default hides only the finished statuses, never a live one', () => {
  const defaults = defaultReviewStatuses();
  // READY is a review waiting on its reviewers; FAILED is a build that broke.
  // Both are work, and hiding either would bury exactly what this filter exists
  // to surface — a FAILED review is the most actionable row a tenant has.
  assert.ok(defaults.includes('READY'));
  assert.ok(defaults.includes('FAILED'));
  // MERGED and CLOSED are the only statuses meaning "nobody's problem any
  // more", and they are what made the unfiltered list unreadable.
  assert.ok(!defaults.includes('MERGED'));
  assert.ok(!defaults.includes('CLOSED'));
});

test('the untouched status set reads as default, and a narrowed one does not', () => {
  assert.equal(reviewStatusFilterIsDefault(['OPEN', 'READY', 'MERGE', 'FAILED']), true);
  // Same members, different order: still the default set.
  assert.equal(reviewStatusFilterIsDefault(['FAILED', 'MERGE', 'READY', 'OPEN']), true);
  assert.equal(reviewStatusFilterIsDefault(['OPEN']), false);
  assert.equal(reviewStatusFilterIsDefault(['OPEN', 'READY', 'MERGE', 'FAILED', 'MERGED']), false);
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

// The parent held `mine` and `waitingOnMe` as a pair, handed both
// to the filter toolbar, and handed the empty state only their `||` — so the
// copy could not say which filter had come up empty and said "whichever
// you've turned on" instead. These pin the naming, and with it the shape: one
// axis at a time, named in the operator's own words.
test('the empty state names the one authorship filter that is on', () => {
  assert.equal(
    reviewsFilteredEmptyBody({ mine: true, waitingOnMe: false, statuses: [] }),
    'Nothing is Mine right now.',
  );
  assert.equal(
    reviewsFilteredEmptyBody({ mine: false, waitingOnMe: true, statuses: [] }),
    'Nothing is Waiting on me right now.',
  );
});

test('the empty state names both authorship filters only when both are on', () => {
  assert.equal(
    reviewsFilteredEmptyBody({ mine: true, waitingOnMe: true, statuses: [] }),
    'Nothing is both Mine and Waiting on me right now.',
  );
});

test('the empty state names the statuses that came up empty', () => {
  assert.equal(
    reviewsFilteredEmptyBody({ mine: false, waitingOnMe: false, statuses: ['MERGED'] }),
    'Nothing has status MERGED right now.',
  );
  assert.equal(
    reviewsFilteredEmptyBody({ mine: false, waitingOnMe: false, statuses: ['MERGED', 'CLOSED'] }),
    'Nothing has status MERGED or CLOSED right now.',
  );
});

test('the empty state names both axes when both are narrowing', () => {
  assert.equal(
    reviewsFilteredEmptyBody({ mine: true, waitingOnMe: false, statuses: ['MERGED'] }),
    'Nothing is Mine right now, with status MERGED.',
  );
});

// The two empty states are chosen by the same predicate the copy is built
// from, so they cannot disagree. An empty status selection and the opening
// default set both show everything, so neither is a filter the operator
// turned on — reading either as one switched a tenant with no reviews to
// "No reviews match this filter", which is the confusion the three-empty-states
// rule exists to prevent.
test('the opening default and an empty status selection narrow nothing', () => {
  assert.equal(
    reviewsFilterIsNarrowing({
      mine: false,
      waitingOnMe: false,
      statuses: defaultReviewStatuses(),
    }),
    false,
  );
  assert.equal(reviewsFilterIsNarrowing({ mine: false, waitingOnMe: false, statuses: [] }), false);
  assert.equal(
    reviewsFilterIsNarrowing({ mine: true, waitingOnMe: false, statuses: defaultReviewStatuses() }),
    true,
  );
});

test('reviewCountLabel names both numbers only when rows are hidden', () => {
  assert.equal(reviewCountLabel(1, 1), '1 review');
  assert.equal(reviewCountLabel(0, 0), '0 reviews');
  assert.equal(reviewCountLabel(3, 155), '3 of 155 reviews');
  assert.equal(reviewCountLabel(1, 155), '1 of 155 reviews');
});
