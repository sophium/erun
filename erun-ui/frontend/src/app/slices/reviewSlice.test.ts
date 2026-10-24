import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { DiffResult } from '@/types';

import reviewReducer, {
  emptyEnvDiffState,
  pruneEnvDiffs,
  type ReviewState,
  setEnvDiff,
  setEnvDiffError,
  setEnvDiffLoading,
  setEnvReviewCommit,
  setEnvReviewScope,
} from './reviewSlice';

function initial(): ReviewState {
  return reviewReducer(undefined, { type: '@@INIT' });
}

function diff(marker: string): DiffResult {
  return { rawDiff: marker, summary: { fileCount: 1, additions: 1, deletions: 0 } };
}

test('a never-fetched environment has no entry in diffByEnv', () => {
  const state = initial();

  assert.equal(state.diffByEnv['a/b'], undefined);
  assert.deepEqual(state.diffByEnv, {});
});

test("setEnvDiff creates only the written environment's slot, defaulted from emptyEnvDiffState", () => {
  const state = reviewReducer(initial(), setEnvDiff({ envKey: 'a/b', diff: diff('x') }));

  assert.deepEqual(state.diffByEnv['a/b'], { ...emptyEnvDiffState, diff: diff('x') });
  assert.equal(state.diffByEnv['c/d'], undefined);
});

test("setEnvDiffError only ever mutates the targeted environment's slot", () => {
  let state = initial();
  state = reviewReducer(state, setEnvDiff({ envKey: 'a/alpha', diff: diff('alpha-diff') }));
  state = reviewReducer(state, setEnvDiff({ envKey: 'a/beta', diff: diff('beta-diff') }));

  state = reviewReducer(
    state,
    setEnvDiffError({ envKey: 'a/alpha', error: 'unreachable', reconnectable: true }),
  );

  const alpha = state.diffByEnv['a/alpha'];
  const beta = state.diffByEnv['a/beta'];
  assert.ok(alpha);
  assert.ok(beta);
  assert.equal(alpha.error, 'unreachable');
  assert.equal(alpha.errorReconnectable, true);
  assert.deepEqual(beta.diff, diff('beta-diff'));
  assert.equal(beta.error, '');
  assert.equal(beta.errorReconnectable, false);
});

test("setEnvDiffLoading only flips the targeted environment's loading flag", () => {
  let state = initial();
  state = reviewReducer(state, setEnvDiff({ envKey: 'a/alpha', diff: diff('alpha-diff') }));
  state = reviewReducer(state, setEnvDiff({ envKey: 'a/beta', diff: diff('beta-diff') }));

  state = reviewReducer(state, setEnvDiffLoading({ envKey: 'a/alpha', loading: true }));

  assert.equal(state.diffByEnv['a/alpha']?.loading, true);
  assert.equal(state.diffByEnv['a/beta']?.loading, false);
});

test('pruneEnvDiffs drops environments outside the kept set and leaves the rest untouched', () => {
  let state = initial();
  state = reviewReducer(state, setEnvDiff({ envKey: 'a/alpha', diff: diff('alpha-diff') }));
  state = reviewReducer(state, setEnvDiff({ envKey: 'a/beta', diff: diff('beta-diff') }));
  state = reviewReducer(state, setEnvDiff({ envKey: 'a/gamma', diff: diff('gamma-diff') }));

  state = reviewReducer(state, pruneEnvDiffs(['a/alpha', 'a/beta']));

  assert.deepEqual(Object.keys(state.diffByEnv).sort(), ['a/alpha', 'a/beta']);
  assert.deepEqual(state.diffByEnv['a/alpha']?.diff, diff('alpha-diff'));
  assert.deepEqual(state.diffByEnv['a/beta']?.diff, diff('beta-diff'));
});

test('scope and commit selection is independent per environment', () => {
  let state = initial();
  state = reviewReducer(state, setEnvReviewScope({ envKey: 'a/alpha', scope: 'all' }));
  state = reviewReducer(state, setEnvReviewCommit({ envKey: 'a/alpha', commit: 'deadbeef' }));

  assert.equal(state.diffByEnv['a/alpha']?.scope, 'all');
  assert.equal(state.diffByEnv['a/alpha'].commit, 'deadbeef');
  assert.equal(state.diffByEnv['a/beta'], undefined);

  state = reviewReducer(state, setEnvReviewScope({ envKey: 'a/beta', scope: 'commit' }));
  state = reviewReducer(state, setEnvReviewCommit({ envKey: 'a/beta', commit: 'cafebabe' }));

  assert.equal(state.diffByEnv['a/alpha']?.scope, 'all');
  assert.equal(state.diffByEnv['a/alpha'].commit, 'deadbeef');
  assert.equal(state.diffByEnv['a/beta']?.scope, 'commit');
  assert.equal(state.diffByEnv['a/beta'].commit, 'cafebabe');
});
