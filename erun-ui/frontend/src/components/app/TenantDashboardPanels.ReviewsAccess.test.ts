import assert from 'node:assert/strict';

import { Tabs } from 'erun-kit';
import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Provider } from 'react-redux';
import { test } from 'vitest';

import { store } from '@/app/store';
import type { UITenantDashboard } from '@/types';

import { ReviewsPanel } from './TenantDashboardPanels.Reviews';

// The capable-caller case is the positive control for the absence assertions.

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

function render(element: React.ReactElement): string {
  return renderToStaticMarkup(React.createElement(Provider, { store, children: element }));
}

function renderReviewsTab(data: UITenantDashboard): string {
  return render(
    React.createElement(
      Tabs,
      { defaultValue: 'reviews' },
      React.createElement(ReviewsPanel, { data }),
    ),
  );
}

test('a caller who may not create a review is not sent to the button that is not rendered', () => {
  const markup = renderReviewsTab(dashboard({ canCreateReview: false }));
  assert.ok(markup.includes('No reviews yet'), 'the empty state itself still renders');
  assert.ok(
    !markup.includes('New review button above'),
    'the empty state must not name the button its own row replaced with a notice',
  );
  assert.ok(
    !markup.includes('erun review create'),
    'the CLI route is refused by the same capability, so it is not advice either',
  );
});

test('a caller who may not create a review is told where the missing access is named', () => {
  const markup = renderReviewsTab(dashboard({ canCreateReview: false }));
  assert.ok(
    markup.includes('Creating a review needs additional access'),
    'the restricted body names the missing access rather than a route',
  );
  assert.ok(
    markup.includes('You do not have access to create reviews.'),
    'the notice the restricted body points at is rendered in the same row',
  );
});

test('a caller who may create a review keeps the sentence naming both routes', () => {
  const markup = renderReviewsTab(dashboard({ canCreateReview: true }));
  // renderToStaticMarkup escapes the apostrophe in "CLI's", so assert in two pieces.
  assert.ok(
    markup.includes('A review appears here once someone opens one from the CLI'),
    'a caller who may create a review is still pointed at the CLI route',
  );
  assert.ok(
    markup.includes('erun review create or the New review button above.'),
    'and at the button, which is on screen for them',
  );
  assert.ok(markup.includes('New review'), 'the button the copy names is rendered for them');
});
