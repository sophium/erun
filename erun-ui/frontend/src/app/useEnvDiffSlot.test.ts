import assert from 'node:assert/strict';
import { test } from 'node:test';

import { configureStore } from '@reduxjs/toolkit';
import * as React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { Provider } from 'react-redux';

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

// renderPair mounts a component that calls the real hook -- always exactly
// twice, unconditionally, so the loop-free call shape satisfies
// react-hooks/rules-of-hooks -- and captures both return values, via
// react-dom/server (no jsdom needed since the hook never touches the DOM).
// This exercises useEnvDiffSlot itself (not a re-implemented copy of its
// selector expression), so a change to the hook's fallback logic is caught
// here. Every case below fits two slots: a lone lookup just repeats its key.
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

// #1178: diffByEnv is sparse, so a key that was never fetched must fall back
// to the shared empty slot rather than throwing or reading undefined fields.
test('a never-fetched environment falls back to the empty slot without throwing', () => {
  const store = storeWith({});

  const [missing] = renderPair(store, 'a/never-fetched', 'a/never-fetched');

  assert.deepEqual(missing, emptyEnvDiffState);
});

// The failure mode this hook exists to prevent: a missing key must never
// resolve to a DIFFERENT environment's data. Probing two keys in the same
// render -- one present, one absent -- pins that the absent one cannot
// silently alias the present one.
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
