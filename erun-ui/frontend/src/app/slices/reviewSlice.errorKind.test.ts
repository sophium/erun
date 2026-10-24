import assert from 'node:assert/strict';

import { test } from 'vitest';

import reviewReducer, { emptyEnvDiffState, type ReviewState, setEnvDiffError } from './reviewSlice';

// errorKind is the discriminator the review panel branches its copy on
// (informational "not-open" vs a fault "stale-forward").

function initial(): ReviewState {
  return reviewReducer(undefined, { type: '@@INIT' });
}

test('emptyEnvDiffState defaults errorKind to stale-forward', () => {
  assert.equal(emptyEnvDiffState.errorKind, 'stale-forward');
});

test('setEnvDiffError without a kind defaults to stale-forward, preserving pre-#1230 always-a-fault callers', () => {
  const state = reviewReducer(
    initial(),
    setEnvDiffError({ envKey: 'a/alpha', error: 'unreachable', reconnectable: true }),
  );

  assert.equal(state.diffByEnv['a/alpha']?.errorKind, 'stale-forward');
});

test('setEnvDiffError threads an explicit not-open kind onto the targeted slot only', () => {
  let state = initial();
  state = reviewReducer(
    state,
    setEnvDiffError({
      envKey: 'a/alpha',
      error: 'not open',
      reconnectable: true,
      kind: 'not-open',
    }),
  );
  state = reviewReducer(
    state,
    setEnvDiffError({
      envKey: 'a/beta',
      error: 'stale',
      reconnectable: true,
      kind: 'stale-forward',
    }),
  );

  assert.equal(state.diffByEnv['a/alpha']?.errorKind, 'not-open');
  assert.equal(state.diffByEnv['a/beta']?.errorKind, 'stale-forward');
});
