import assert from 'node:assert/strict';

import { configureStore } from '@reduxjs/toolkit';
import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Provider } from 'react-redux';
import { test } from 'vitest';

import reviewReducer, {
  emptyEnvDiffState,
  setEnvDiff,
  setEnvDiffError,
} from './slices/reviewSlice';
import { useEnvDiffSlot } from './useEnvDiffSlot';

function storeWith(entries: Record<string, { diff: unknown; error?: string }>) {
  const store = configureStore({ reducer: { review: reviewReducer } });
  for (const [envKey, entry] of Object.entries(entries)) {
    store.dispatch(setEnvDiff({ envKey, diff: entry.diff as never }));
    if (entry.error) {
      store.dispatch(setEnvDiffError({ envKey, error: entry.error, reconnectable: false }));
    }
  }
  return store;
}

// The component calls the hook exactly twice, unconditionally, for rules-of-hooks.
function renderPair(
  store: ReturnType<typeof storeWith>,
  envKeyA: string,
  envKeyB: string,
): [unknown, unknown] {
  let captured: [unknown, unknown] = [undefined, undefined];
  function Probe(): React.ReactElement {
    captured = [useEnvDiffSlot(envKeyA), useEnvDiffSlot(envKeyB)];
    return React.createElement('div');
  }
  renderToStaticMarkup(
    React.createElement(Provider, { store, children: React.createElement(Probe) }),
  );
  return captured;
}

test('a fetched environment returns its own slot', () => {
  const store = storeWith({ 'a/alpha': { diff: { marker: 'alpha' } } });

  const [alpha] = renderPair(store, 'a/alpha', 'a/alpha');

  assert.deepEqual(alpha, { ...emptyEnvDiffState, diff: { marker: 'alpha' } });
});

test('a never-fetched environment falls back to the empty slot without throwing', () => {
  const store = storeWith({});

  const [missing] = renderPair(store, 'a/never-fetched', 'a/never-fetched');

  assert.deepEqual(missing, emptyEnvDiffState);
});

test("a missing environment never aliases a different environment's slot", () => {
  const store = storeWith({ 'a/alpha': { diff: { marker: 'alpha-only' } } });

  const [alpha, beta] = renderPair(store, 'a/alpha', 'a/beta');

  assert.deepEqual(alpha, { ...emptyEnvDiffState, diff: { marker: 'alpha-only' } });
  assert.deepEqual(beta, emptyEnvDiffState);
  assert.notDeepEqual(beta, alpha);
});

test("one environment's error does not appear on another environment's slot", () => {
  const store = storeWith({
    'a/alpha': { diff: { marker: 'alpha' }, error: 'alpha unreachable' },
    'a/beta': { diff: { marker: 'beta' } },
  });

  const [alpha, beta] = renderPair(store, 'a/alpha', 'a/beta');

  assert.equal((alpha as { error: string }).error, 'alpha unreachable');
  assert.equal((beta as { error: string }).error, '');
});
