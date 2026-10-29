import assert from 'node:assert/strict';

import { test } from 'vitest';

import { selectReviewTargets } from './selectors';
import type { OrchestratorInfo } from './slices/orchestratorsSlice';
import type { RootState } from './store';

function orchestrator(
  id: string,
  sessionId: number,
  environments: OrchestratorInfo['environments'],
  directories: string[] = [],
) {
  return {
    id,
    name: id,
    environments,
    tenants: [...new Set(environments.map((env) => env.tenant))],
    // The orchestrator's own directories, not the linked environments'.
    directories,
    sessionId,
    status: sessionId > 0 ? 'running' : 'stopped',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
    nudgeCount: 0,
    autoNudgeCount: 0,
    whipCount: 0,
    nudgeCapped: false,
    restartRequired: false,
    roleChanged: false,
    launchSettingsChanged: false,
  } satisfies OrchestratorInfo;
}

function stateWith(fields: {
  sessionId?: number;
  orchestrators?: OrchestratorInfo[];
  selected?: { tenant: string; environment: string } | null;
}): RootState {
  return {
    terminal: { sessionId: fields.sessionId ?? 0 },
    orchestrators: { items: fields.orchestrators ?? [] },
    selection: { selected: fields.selected ?? null },
  } as unknown as RootState;
}

test('an active orchestrator session resolves its linked environments as targets, in order', () => {
  const state = stateWith({
    sessionId: 42,
    orchestrators: [
      orchestrator('erun-issues', 42, [
        { tenant: 'acme', environment: 'alpha', directory: '/tmp/alpha', role: '' },
        { tenant: 'acme', environment: 'beta', directory: '/tmp/beta', role: '' },
      ]),
    ],
  });

  const targets = selectReviewTargets(state);

  assert.deepEqual(targets, [
    { kind: 'env', envKey: 'acme/alpha', tenant: 'acme', environment: 'alpha' },
    { kind: 'env', envKey: 'acme/beta', tenant: 'acme', environment: 'beta' },
  ]);
});

test('an environment tab (no active orchestrator) resolves the sidebar selection as the sole target', () => {
  const state = stateWith({ selected: { tenant: 'acme', environment: 'alpha' } });

  const targets = selectReviewTargets(state);

  assert.deepEqual(targets, [
    { kind: 'env', envKey: 'acme/alpha', tenant: 'acme', environment: 'alpha' },
  ]);
});

test('nothing selected and no active orchestrator resolves no targets', () => {
  const state = stateWith({});

  assert.deepEqual(selectReviewTargets(state), []);
});

test('an active orchestrator session ignores a stale sidebar environment selection', () => {
  const state = stateWith({
    sessionId: 42,
    orchestrators: [
      orchestrator('erun-issues', 42, [
        { tenant: 'acme', environment: 'alpha', directory: '/tmp/alpha', role: '' },
      ]),
    ],
    selected: { tenant: 'acme', environment: 'unrelated' },
  });

  const targets = selectReviewTargets(state);

  assert.deepEqual(targets, [
    { kind: 'env', envKey: 'acme/alpha', tenant: 'acme', environment: 'alpha' },
  ]);
});

test('a stopped orchestrator contributes no targets', () => {
  const state = stateWith({
    sessionId: 0,
    orchestrators: [
      orchestrator('erun-issues', 0, [
        { tenant: 'acme', environment: 'alpha', directory: '/tmp/alpha', role: '' },
      ]),
    ],
  });

  assert.deepEqual(selectReviewTargets(state), []);
});

test('an orchestrator whose only scope is directories resolves those as targets', () => {
  const state = stateWith({
    sessionId: 42,
    orchestrators: [
      orchestrator('scratch', 42, [], ['/Users/op/src/notes', '/Users/op/src/scratch']),
    ],
  });

  const targets = selectReviewTargets(state);

  assert.deepEqual(targets, [
    {
      kind: 'directory',
      envKey: 'directory:/Users/op/src/notes',
      directory: '/Users/op/src/notes',
    },
    {
      kind: 'directory',
      envKey: 'directory:/Users/op/src/scratch',
      directory: '/Users/op/src/scratch',
    },
  ]);
});

test('a linked environment and a directory both resolve, environments first', () => {
  const state = stateWith({
    sessionId: 42,
    orchestrators: [
      orchestrator(
        'mixed',
        42,
        [{ tenant: 'acme', environment: 'alpha', directory: '/tmp/alpha', role: '' }],
        ['/Users/op/src/scratch'],
      ),
    ],
  });

  const targets = selectReviewTargets(state);

  assert.deepEqual(targets, [
    { kind: 'env', envKey: 'acme/alpha', tenant: 'acme', environment: 'alpha' },
    {
      kind: 'directory',
      envKey: 'directory:/Users/op/src/scratch',
      directory: '/Users/op/src/scratch',
    },
  ]);
});
