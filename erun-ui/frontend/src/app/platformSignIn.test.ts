import assert from 'node:assert/strict';
import { test } from 'node:test';

import type { UITenantDashboard } from '@/types';

import {
  resolveTenantPlatformAlias,
  TENANT_SIGN_IN_AGAIN_MESSAGE,
  tenantNeedsSignIn,
} from './platformSignIn';

// These pin the exact sentence a review write's stale-identity failure
// resolves to a sign-in action, and that every other message — the "ask an
// administrator" permission notices included — stays action-less. The
// dashboard's own whole-dashboard load no longer goes through this
// string-matching contract at all — it renders directly off
// UITenantDashboard.platformState.

test('the known stale-identity sentence needs a sign-in action', () => {
  assert.equal(tenantNeedsSignIn(TENANT_SIGN_IN_AGAIN_MESSAGE), true);
});

test('an unrelated message, including a permission refusal, needs no action', () => {
  assert.equal(
    tenantNeedsSignIn(
      'You do not have access to this tenant’s dashboard. Ask an administrator for access.',
    ),
    false,
  );
  assert.equal(tenantNeedsSignIn('load tenant dashboard GET /v1/reviews: network error'), false);
  assert.equal(tenantNeedsSignIn(''), false);
});

const dashboard: UITenantDashboard = {
  tenant: 'frs',
  canCreateReview: false,
  canAdvanceMergeQueue: false,
  canOverrideMergeQueue: false,
  platformAlias: 'erun+api.frs-prod.services.erunpaas.com@erun',
};

test('resolveTenantPlatformAlias reads the dashboard’s own resolved platform alias', () => {
  assert.equal(
    resolveTenantPlatformAlias(dashboard),
    'erun+api.frs-prod.services.erunpaas.com@erun',
  );
});

test('resolveTenantPlatformAlias returns empty when no dashboard is loaded', () => {
  assert.equal(resolveTenantPlatformAlias(null), '');
  assert.equal(resolveTenantPlatformAlias(undefined), '');
});
