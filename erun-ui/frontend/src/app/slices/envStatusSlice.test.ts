import assert from 'node:assert/strict';
import { test } from 'node:test';

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

// The sidebar's busy row clears only when envObserved flips to true (see
// environmentRowIsBusy in Sidebar.helpers.ts). A transition that changes
// nothing else about the observation must still be recorded, or the row can
// never learn the environment answered.
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
