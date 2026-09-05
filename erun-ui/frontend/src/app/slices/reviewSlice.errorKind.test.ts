import assert from 'node:assert/strict';
import { test } from 'node:test';

import reviewReducer, { emptyEnvDiffState, type ReviewState, setEnvDiffError } from './reviewSlice';

// #1230: errorKind is the discriminator the review panel branches its copy
// on (informational "not-open" vs a fault "stale-forward"). This is a
// separate file from reviewSlice.test.ts (the #1244 characterization spec for
// this slice) rather than an edit to it, per the mandate to leave that file
// untouched.

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
