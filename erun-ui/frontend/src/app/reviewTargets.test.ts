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
    // The orchestrator's own directories, which belong to no environment --
    // deliberately NOT the linked environments' directories, which is what this
    // used to derive and what the payload no longer means.
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
  } satisfies OrchestratorInfo;
}

// Only the three slices selectReviewTargets reads.
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

// An orchestrator session resolves the diff panel's targets from its
// linked environments, in their configured order -- the order the diff panel
// renders sections in, and the order ReviewRangeControls scopes its per-env
// controls by.
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

// A single-environment tab is the one-entry case of the same resolution: no
// orchestrator session active, just the sidebar's selected environment.
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

// The active SESSION decides, not the sidebar's environment selection: with
// an orchestrator's session active, a stale sidebar selection must not also
// contribute a target, or the panel would show a section for an environment
// the orchestrator isn't even linked to.
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

// A stopped orchestrator (sessionId 0) must not contribute targets even
// though its definition still links environments.
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

// An orchestrator that links no environment at all and names only directories is
// a definition the desktop accepts, and the panel used to read "No environment
// selected" for it -- the whole review surface missing for a scope the
// orchestrator actually works in. Each directory resolves to a target of its own.
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

// Both kinds together: the environments an orchestrator links and the
// directories it works in are in scope at once, environments first, so a session
// that has both reads as one panel rather than one kind hiding the other.
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
