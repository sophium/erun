import assert from 'node:assert/strict';

import { test } from 'vitest';

import reducer, { type EnvObservedActivity, setEnvActivityForEnv } from './envStatusSlice';

function activity(overrides: Partial<EnvObservedActivity> = {}): EnvObservedActivity {
  return {
    reachable: true,
    observed: false,
    outage: false,
    checkFailed: false,
    busy: false,
    detail: '',
    ...overrides,
  };
}

// The busy row clears only when envObserved flips (environmentRowIsBusy).
test('a transition where only observed changes is not swallowed as unchanged', () => {
  const seeded = reducer(undefined, setEnvActivityForEnv({ key: 'k', activity: activity() }));

  const next = reducer(
    seeded,
    setEnvActivityForEnv({ key: 'k', activity: activity({ observed: true }) }),
  );

  assert.equal(next.activityByEnv.k?.observed, true);
});

test('an identical repeat is still suppressed', () => {
  const seeded = reducer(
    undefined,
    setEnvActivityForEnv({ key: 'k', activity: activity({ observed: true }) }),
  );

  const next = reducer(
    seeded,
    setEnvActivityForEnv({ key: 'k', activity: activity({ observed: true }) }),
  );

  assert.equal(next.activityByEnv.k, seeded.activityByEnv.k);
});
