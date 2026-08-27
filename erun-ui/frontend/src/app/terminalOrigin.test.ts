import assert from 'node:assert/strict';
import { test } from 'node:test';

import type { TerminalTab } from './state';
import type { RootState } from './store';
import { terminalOriginForSession } from './terminalOrigin';
import { selectionKey } from './versionSuggestions';

const ENV_KEY = selectionKey({ tenant: 'acme', environment: 'dev' });

// Only the two slices terminalOriginForSession reads; the rest of RootState is
// irrelevant to it and constructing it would make the test about the store's
// shape instead.
function stateWith(
  orchestratorSessionIds: number[],
  tabsByEnv: Record<string, TerminalTab[]>,
): RootState {
  return {
    orchestrators: { items: orchestratorSessionIds.map((sessionId) => ({ sessionId })) },
    terminal: { tabsByEnv },
  } as unknown as RootState;
}

function tab(sessionId: number, kind: TerminalTab['kind']): TerminalTab {
  return { sessionId, slot: 0, kind, label: kind };
}

test('an orchestrator session is host-side', () => {
  const state = stateWith([42], {});
  assert.deepEqual(terminalOriginForSession(state, 42), { kind: 'host' });
});

test('the Local tab is host-side', () => {
  const state = stateWith([], { [ENV_KEY]: [tab(5, 'local')] });
  assert.deepEqual(terminalOriginForSession(state, 5), { kind: 'host' });
});

test('the contribute shell tabs are host-side', () => {
  const state = stateWith([], {
    [ENV_KEY]: [tab(5, 'contribute-erun'), tab(6, 'contribute-ai')],
  });
  assert.deepEqual(terminalOriginForSession(state, 5), { kind: 'host' });
  assert.deepEqual(terminalOriginForSession(state, 6), { kind: 'host' });
});

// The regression this exists to prevent: an environment's ERun/AI/extra tabs
// run inside that environment's pod, so a path they print must resolve there,
// never against the host.
test('an environment ERun, AI, or extra tab is pod-side, scoped to its environment', () => {
  const state = stateWith([], {
    [ENV_KEY]: [tab(5, 'erun'), tab(6, 'ai'), tab(7, 'extra')],
  });
  assert.deepEqual(terminalOriginForSession(state, 5), {
    kind: 'pod',
    tenant: 'acme',
    environment: 'dev',
  });
  assert.deepEqual(terminalOriginForSession(state, 6), {
    kind: 'pod',
    tenant: 'acme',
    environment: 'dev',
  });
  assert.deepEqual(terminalOriginForSession(state, 7), {
    kind: 'pod',
    tenant: 'acme',
    environment: 'dev',
  });
});

test('an untracked session id is unknown, never defaulted to host', () => {
  const state = stateWith([42], { [ENV_KEY]: [tab(5, 'erun')] });
  assert.deepEqual(terminalOriginForSession(state, 999), { kind: 'unknown' });
});

test('sessionId 0 (no active session) is host-side', () => {
  const state = stateWith([], {});
  assert.deepEqual(terminalOriginForSession(state, 0), { kind: 'host' });
});
