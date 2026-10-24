import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UITenantDashboard } from '@/types';

import {
  resolveTenantPlatformAlias,
  TENANT_SIGN_IN_AGAIN_MESSAGE,
  tenantNeedsSignIn,
} from './platformSignIn';

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
  canCreateContext: false,
  canRegisterEnvironment: false,
  canDeployEnvironment: false,
  canStopEnvironment: false,
  canDeleteEnvironment: false,
  canApproveInviteRequests: false,
  canDeclineInviteRequests: false,
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
