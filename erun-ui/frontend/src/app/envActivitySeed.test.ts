import assert from 'node:assert/strict';
import { test } from 'node:test';

import type { UITenant } from '@/types';

import { planEnvActivitySeed } from './envActivitySeed';
import { selectionKey } from './versionSuggestions';

function tenant(name: string, environments: UITenant['environments']): UITenant {
  return { name, environments };
}

// The bug (erun#1216): the env-activity Wails event only fires on a
// transition, so a boot with no prior transition to replay (a page reload
// via ErrorBoundary's "Reload app", not a process restart) had nothing to
// seed a still-busy env's row from. The fix is that uiEnvironment now carries
// the poller's last observation directly, so a fetch alone reproduces the
// correct seed with no event required — the same shape planOrchestratorBusySeed
// already uses for orchestrator rows.
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
