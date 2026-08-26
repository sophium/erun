import assert from 'node:assert/strict';
import { test } from 'node:test';

import type { UITenant } from '@/types';

import {
  resolveTenantCloudAlias,
  TENANT_IDENTITY_SIGN_IN_MESSAGE,
  TENANT_SIGN_IN_AGAIN_MESSAGE,
  tenantNeedsSignIn,
} from './platformSignIn';

// These pin the exact two sentences the four dead-end error messages in
// #1390 resolve to a sign-in action, and that every other message — the
// "ask an administrator" permission notices included — stays action-less.

test('the two known stale-identity sentences need a sign-in action', () => {
  assert.equal(tenantNeedsSignIn(TENANT_IDENTITY_SIGN_IN_MESSAGE), true);
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

const tenants: UITenant[] = [
  {
    name: 'frs',
    environments: [],
    defaultEnvironment: '',
    primaryCloudProviderAlias: 'frs-aws',
  },
];

test('resolveTenantCloudAlias finds the named tenant’s primary alias', () => {
  assert.equal(resolveTenantCloudAlias(tenants, 'frs'), 'frs-aws');
});

test('resolveTenantCloudAlias returns empty for an unknown tenant', () => {
  assert.equal(resolveTenantCloudAlias(tenants, 'unknown'), '');
});
