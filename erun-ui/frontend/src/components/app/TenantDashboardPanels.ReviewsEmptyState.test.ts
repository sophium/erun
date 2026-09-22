import assert from 'node:assert/strict';

import { Tabs } from 'erun-kit';
import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Provider } from 'react-redux';
import { afterEach, test } from 'vitest';

import { defaultReviewFilter } from '@/app/reviewDetailState';
import { patchTenantDashboard } from '@/app/slices/tenantDashboardSlice';
import { store } from '@/app/store';
import type { UITenantDashboard } from '@/types';

import { ReviewsPanel } from './TenantDashboardPanels.Reviews';

// With only the Mine chip on and nothing matching, the reviews empty state
// used to read "Nothing is both Mine and Waiting on me right now, whichever
// you've turned on." — a conjunction of two filters describing a query where
// one was applied, hedged to cover the mismatch. The component could not do
// better because it was handed `mine || waitingOnMe`: the panel passed the pair
// to its filter toolbar and only their disjunction to the empty state.
//
// These render the real panel from the real store with one filter set, so the
// assertion is on the copy an operator actually sees, and a regression that
// went back to a boolean would fail here. The no-filter case is the positive
// control: a body that named a filter unconditionally would pass the first two
// assertions but not this one.

function dashboard(overrides: Partial<UITenantDashboard> = {}): UITenantDashboard {
  return {
    tenant: 'frs',
    reviews: [],
    panels: [],
    canCreateReview: false,
    canAdvanceMergeQueue: false,
    canOverrideMergeQueue: false,
    canCreateContext: false,
    canRegisterEnvironment: false,
    canDeployEnvironment: false,
    canStopEnvironment: false,
    canDeleteEnvironment: false,
    canApproveInviteRequests: false,
    canDeclineInviteRequests: false,
    ...overrides,
  };
}

function renderReviewsTab(data: UITenantDashboard): string {
  return renderToStaticMarkup(
    React.createElement(
      Provider,
      { store, children: null },
      React.createElement(
        Tabs,
        { defaultValue: 'reviews' },
        React.createElement(ReviewsPanel, { data }),
      ),
    ),
  );
}

// The panel renders the filter toolbar beside the empty state, so "Waiting on
// me" is on the page whether or not the empty state names it. These read the
// empty state's own sentence: everything from its opening "Nothing" up to the
// first full stop.
function emptyStateSentence(markup: string): string {
  return /Nothing [^<]*?\./.exec(markup)?.[0] ?? '';
}

// The store is a module singleton shared by this file's renders, so a filter
// left set by one case would narrow the next one's panel.
afterEach(() => {
  store.dispatch(patchTenantDashboard({ reviewFilter: defaultReviewFilter() }));
});

test('only Mine on names Mine, and nothing else', () => {
  store.dispatch(
    patchTenantDashboard({ reviewFilter: { mine: true, waitingOnMe: false, statuses: [] } }),
  );
  const markup = renderReviewsTab(dashboard());
  assert.equal(emptyStateSentence(markup), 'Nothing is Mine right now.');
  assert.ok(
    !markup.includes('whichever'),
    'the hedge is what covered the mismatch, so it must be gone',
  );
});

test('only Waiting on me on names Waiting on me, and nothing else', () => {
  store.dispatch(
    patchTenantDashboard({ reviewFilter: { mine: false, waitingOnMe: true, statuses: [] } }),
  );
  const markup = renderReviewsTab(dashboard());
  assert.equal(emptyStateSentence(markup), 'Nothing is Waiting on me right now.');
});

test('both authorship filters on name both', () => {
  store.dispatch(
    patchTenantDashboard({ reviewFilter: { mine: true, waitingOnMe: true, statuses: [] } }),
  );
  const markup = renderReviewsTab(dashboard());
  assert.equal(emptyStateSentence(markup), 'Nothing is both Mine and Waiting on me right now.');
});

test('a narrowed status set is named by its own statuses', () => {
  store.dispatch(
    patchTenantDashboard({
      reviewFilter: { mine: false, waitingOnMe: false, statuses: ['MERGED'] },
    }),
  );
  const markup = renderReviewsTab(dashboard());
  assert.equal(emptyStateSentence(markup), 'Nothing has status MERGED right now.');
});

// The opening state is not a filter the operator turned on: nothing is hiding
// rows, so the panel must report the tenant's own empty state rather than a
// filtered one. Passing the default status set through as if it were a filter
// is what would flip this to "No reviews match this filter".
test('with no filter set the panel reports an empty tenant, not a filtered zero', () => {
  const markup = renderReviewsTab(dashboard());
  assert.ok(markup.includes('No reviews yet'), markup);
  assert.ok(!markup.includes('No reviews match this filter'), markup);
});

// Turning every status chip off is how the unfiltered list is reached, so it
// hides nothing — but the old filterActive test read it as a filter and showed
// "No reviews match this filter" over a list that was showing everything.
test('an empty status selection is the unfiltered list, not a filter', () => {
  store.dispatch(
    patchTenantDashboard({ reviewFilter: { mine: false, waitingOnMe: false, statuses: [] } }),
  );
  const markup = renderReviewsTab(dashboard());
  assert.ok(markup.includes('No reviews yet'), markup);
  assert.ok(!markup.includes('No reviews match this filter'), markup);
});
