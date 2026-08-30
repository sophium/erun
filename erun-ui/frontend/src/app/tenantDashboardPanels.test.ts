import assert from 'node:assert/strict';
import { test } from 'node:test';

import type { UITenantDashboard, UITenantDashboardPanel } from '@/types';

import {
  activeTenantDashboardTab,
  middleEllipsis,
  relativeDashboardDate,
  restrictedTenantDashboardReads,
  reviewAuthorInitials,
  visibleTenantDashboardTabs,
} from './tenantDashboardPanels';

// The dashboard's tab strip is the surface that used to render "you may not look
// at this" and "there is nothing here" identically. These pin the difference.

function dashboard(panels: UITenantDashboardPanel[]): UITenantDashboard {
  return {
    tenant: 'frs',
    panels,
    canCreateReview: false,
    canAdvanceMergeQueue: false,
    canOverrideMergeQueue: false,
    canCreateContext: false,
    canRegisterEnvironment: false,
    canPreviewProvision: false,
    canDeployEnvironment: false,
    canStopEnvironment: false,
    canDeleteEnvironment: false,
    canApproveInviteRequests: false,
    canDeclineInviteRequests: false,
  };
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
    ['users', 'reviews', 'builds', 'registration', 'requests', 'api-log'],
  );
});

test('a panel that failed still renders its tab, so the failure is visible', () => {
  const data = dashboard([{ tab: 'users' }, { tab: 'audit', error: 'load failed: http 500' }]);
  assert.ok(visibleTenantDashboardTabs(data).some((descriptor) => descriptor.tab === 'audit'));
});

test('a dashboard that reported no panels keeps every tab', () => {
  // An unknown permission is not a denied one: before the load answers, nothing
  // may be hidden.
  assert.equal(visibleTenantDashboardTabs(null).length, 8);
  assert.equal(
    visibleTenantDashboardTabs({
      tenant: 'frs',
      canCreateReview: false,
      canAdvanceMergeQueue: false,
      canOverrideMergeQueue: false,
      canCreateContext: false,
      canRegisterEnvironment: false,
      canPreviewProvision: false,
      canDeployEnvironment: false,
      canStopEnvironment: false,
      canDeleteEnvironment: false,
      canApproveInviteRequests: false,
      canDeclineInviteRequests: false,
    }).length,
    8,
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

test('relativeDashboardDate reads as a scannable relative phrase, not a raw timestamp', () => {
  const anHourAgo = new Date(Date.now() - 60 * 60 * 1000).toISOString();
  assert.match(relativeDashboardDate(anHourAgo), /ago$/);
  assert.equal(relativeDashboardDate(undefined), '-');
  assert.equal(relativeDashboardDate('not a date'), 'not a date');
});

test('reviewAuthorInitials derives up to two letters from a display name', () => {
  assert.equal(reviewAuthorInitials('You'), 'Y');
  assert.equal(reviewAuthorInitials('reviewer-1'), 'R1');
  assert.equal(reviewAuthorInitials('operator'), 'OP');
  assert.equal(reviewAuthorInitials(''), '?');
});

test('middleEllipsis keeps both ends of a long identifier visible', () => {
  const longBranch =
    'feature/1378-desktop-review-loop-usability-and-craft-pass-for-the-tenant-dashboard-reviews-tab';
  const shortened = middleEllipsis(longBranch);
  assert.ok(shortened.startsWith('feature/1378-desktop'));
  assert.ok(shortened.endsWith('reviews-tab'));
  assert.ok(shortened.includes('…'));
  assert.ok(shortened.length < longBranch.length);
  assert.equal(middleEllipsis('main'), 'main');
});
