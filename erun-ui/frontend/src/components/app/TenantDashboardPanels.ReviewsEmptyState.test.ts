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

// The no-filter case is the positive control.

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

// The toolbar also renders "Waiting on me", so read only the empty state's own sentence.
function emptyStateSentence(markup: string): string {
  return /Nothing [^<]*?\./.exec(markup)?.[0] ?? '';
}

// The store is a module singleton shared across renders.
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

test('with no filter set the panel reports an empty tenant, not a filtered zero', () => {
  const markup = renderReviewsTab(dashboard());
  assert.ok(markup.includes('No reviews yet'), markup);
  assert.ok(!markup.includes('No reviews match this filter'), markup);
});

test('an empty status selection is the unfiltered list, not a filter', () => {
  store.dispatch(
    patchTenantDashboard({ reviewFilter: { mine: false, waitingOnMe: false, statuses: [] } }),
  );
  const markup = renderReviewsTab(dashboard());
  assert.ok(markup.includes('No reviews yet'), markup);
  assert.ok(!markup.includes('No reviews match this filter'), markup);
});
