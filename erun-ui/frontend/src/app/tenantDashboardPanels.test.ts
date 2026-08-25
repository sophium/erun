import assert from 'node:assert/strict';
import { test } from 'node:test';

import type { UITenantDashboard, UITenantDashboardPanel } from '@/types';

import {
  activeTenantDashboardTab,
  restrictedTenantDashboardReads,
  visibleTenantDashboardTabs,
} from './tenantDashboardPanels';

// The dashboard's tab strip is the surface that used to render "you may not look
// at this" and "there is nothing here" identically. These pin the difference.

function dashboard(panels: UITenantDashboardPanel[]): UITenantDashboard {
  return { tenant: 'frs', panels, canCreateReview: false, canAdvanceMergeQueue: false };
}

test('a tab the user may not open does not render', () => {
  const data = dashboard([
    { tab: 'users' },
    { tab: 'reviews' },
    { tab: 'queue', restricted: 'GET /v1/reviews/merge-queue' },
    { tab: 'builds' },
    { tab: 'audit', restricted: 'GET /v1/audit-events' },
  ]);
  assert.deepEqual(
    visibleTenantDashboardTabs(data).map((descriptor) => descriptor.tab),
    ['users', 'reviews', 'builds', 'api-log'],
  );
});

test('a panel that failed still renders its tab, so the failure is visible', () => {
  const data = dashboard([{ tab: 'users' }, { tab: 'audit', error: 'load failed: http 500' }]);
  assert.ok(visibleTenantDashboardTabs(data).some((descriptor) => descriptor.tab === 'audit'));
});

test('a dashboard that reported no panels keeps every tab', () => {
  // An unknown permission is not a denied one: before the load answers, nothing
  // may be hidden.
  assert.equal(visibleTenantDashboardTabs(null).length, 6);
  assert.equal(
    visibleTenantDashboardTabs({
      tenant: 'frs',
      canCreateReview: false,
      canAdvanceMergeQueue: false,
    }).length,
    6,
  );
});

test('the missing access is named rather than left to be guessed', () => {
  const data = dashboard([
    { tab: 'queue', restricted: 'GET /v1/reviews/merge-queue' },
    { tab: 'builds', restricted: 'GET /v1/reviews' },
    { tab: 'audit', restricted: 'GET /v1/reviews/merge-queue' },
  ]);
  assert.deepEqual(restrictedTenantDashboardReads(data), [
    'GET /v1/reviews/merge-queue',
    'GET /v1/reviews',
  ]);
});

test('a selected tab the user may not open falls back to one they can', () => {
  const data = dashboard([
    { tab: 'users', restricted: 'GET /v1/whoami' },
    { tab: 'reviews', restricted: 'GET /v1/reviews' },
    { tab: 'queue' },
  ]);
  assert.equal(activeTenantDashboardTab(data, 'users'), 'queue');
});

test('a selected tab the user may open is kept', () => {
  const data = dashboard([{ tab: 'users' }, { tab: 'queue' }]);
  assert.equal(activeTenantDashboardTab(data, 'queue'), 'queue');
});
