import assert from 'node:assert/strict';

import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Provider } from 'react-redux';
import { test } from 'vitest';

import { store } from '@/app/store';
import { tenantDashboardTabs } from '@/app/tenantDashboardPanels';
import type { UITenantDashboard } from '@/types';

import { TenantPlatformStateCard } from './TenantPlatformState';

// The not-connected card replaces the entire tenant dashboard -- the view
// returns it before the tab strip is ever rendered -- so no tab loads in this
// state, whichever tabs the signed-in caller would otherwise see. The body copy
// used to name five tabs, which read as a complete account of what was
// unavailable and left the rest looking usable. These pin the rendered string:
// the card must not name a subset of the descriptor list, because a subset
// claim is false here for every caller and drifts as tabs are added.

function dashboard(overrides: Partial<UITenantDashboard> = {}): UITenantDashboard {
  return {
    tenant: 'frs',
    contexts: [],
    environments: [],
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
    platformState: 'not-connected',
    ...overrides,
  };
}

function renderNotConnected(data: UITenantDashboard = dashboard()): string {
  return renderToStaticMarkup(
    React.createElement(Provider, {
      store,
      children: React.createElement(TenantPlatformStateCard, { data }),
    }),
  );
}

test('the not-connected card does not name a subset of the dashboard tabs', () => {
  const markup = renderNotConnected();
  const named = tenantDashboardTabs
    .map((descriptor) => descriptor.label)
    .filter((label) => markup.includes(label));
  assert.deepEqual(
    named,
    [],
    'the replaced dashboard cannot load any of its tabs, so naming some of them reads as the others still working',
  );
});

test('the not-connected card says the whole dashboard is unavailable', () => {
  const markup = renderNotConnected();
  assert.ok(
    markup.includes('none of this dashboard'),
    'the copy states that the entire dashboard is replaced, without enumerating a subset',
  );
});
