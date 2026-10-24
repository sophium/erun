import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UITenant } from '@/types';

import { planEnvActivitySeed } from './envActivitySeed';
import { selectionKey } from './versionSuggestions';

function tenant(name: string, environments: UITenant['environments']): UITenant {
  return { name, environments };
}

test('an env with a busy observation seeds its activity from the snapshot alone', () => {
  const seed = planEnvActivitySeed([
    tenant('acme', [
      {
        name: 'dev',
        activity: { reachable: true, observed: true, outage: false, busy: true, detail: 'ai' },
      },
    ]),
  ]);
  assert.deepEqual(seed, [
    {
      key: selectionKey({ tenant: 'acme', environment: 'dev' }),
      activity: {
        reachable: true,
        observed: true,
        outage: false,
        checkFailed: false,
        busy: true,
        detail: 'ai',
      },
    },
  ]);
});

test('an env the poller has never observed contributes no seed', () => {
  assert.deepEqual(planEnvActivitySeed([tenant('acme', [{ name: 'dev' }])]), []);
});

test('mixed observed and unobserved envs across tenants seed only the observed ones', () => {
  const seed = planEnvActivitySeed([
    tenant('acme', [
      { name: 'dev', activity: { reachable: true, observed: true, outage: false, busy: false } },
      { name: 'staging' },
    ]),
    tenant('other', [
      { name: 'main', activity: { reachable: false, observed: false, outage: true, busy: false } },
    ]),
  ]);
  assert.deepEqual(seed, [
    {
      key: selectionKey({ tenant: 'acme', environment: 'dev' }),
      activity: {
        reachable: true,
        observed: true,
        outage: false,
        checkFailed: false,
        busy: false,
        detail: '',
      },
    },
    {
      key: selectionKey({ tenant: 'other', environment: 'main' }),
      activity: {
        reachable: false,
        observed: false,
        outage: true,
        checkFailed: false,
        busy: false,
        detail: '',
      },
    },
  ]);
});
