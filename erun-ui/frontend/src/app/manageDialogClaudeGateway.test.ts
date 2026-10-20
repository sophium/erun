import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UIEnvironmentConfig } from '@/types';

import {
  aiSessionLaunchSignature,
  compactClaudeDraft,
  manageDialogTabHasUnsavedChanges,
  nextPendingRedeploy,
} from './manageDialogHelpers';

// The reported defect: "Use the gateway" could not be set to Disabled (or
// Enabled). Every AI-tab edit is merged and compacted, and the compaction kept
// every Claude field but useGateway, so the choice was dropped before it reached
// the draft, the dirty check, or the save.

function withClaude(
  claude: UIEnvironmentConfig['claude'],
  aiLaunchSignature = '',
): UIEnvironmentConfig {
  return { claude, aiLaunchSignature } as UIEnvironmentConfig;
}

test('choosing Disabled for the gateway survives the draft', () => {
  assert.deepEqual(compactClaudeDraft({ useGateway: false }), { useGateway: false });
});

test('choosing Enabled for the gateway survives the draft', () => {
  assert.deepEqual(compactClaudeDraft({ useGateway: true }), { useGateway: true });
});

test('choosing Default for the gateway clears the override', () => {
  assert.deepEqual(compactClaudeDraft({ useGateway: undefined }), {});
});

test('another AI-tab edit keeps an already-saved gateway choice', () => {
  // The same drop erased a persisted usegateway on the next unrelated save.
  const loaded: UIEnvironmentConfig['claude'] = { useGateway: false };
  assert.deepEqual(compactClaudeDraft({ ...loaded, effort: 'low' }), {
    useGateway: false,
    effort: 'low',
  });
});

test('selecting Disabled marks the AI tab as having unsaved changes', () => {
  const initial = withClaude({});
  const edited = withClaude(compactClaudeDraft({ ...initial.claude, useGateway: false }));
  assert.equal(manageDialogTabHasUnsavedChanges('ai', edited, initial), true);
});

test('a loaded gateway choice compacts back to the same draft, so it is not dirty', () => {
  // The dirty check compares serialized drafts, so the compacted keys must keep
  // the order the backend sends (useMantle, useBedrock, useGateway, models, …).
  const initial = withClaude({ useBedrock: true, useGateway: false, models: ['opus'] });
  const reEdited = withClaude(compactClaudeDraft({ ...initial.claude, effort: undefined }));
  assert.equal(manageDialogTabHasUnsavedChanges('ai', reEdited, initial), false);
});

test('a gateway change raises the pending-redeploy banner', () => {
  // useGateway decides whether the chart renders the gateway and whether deploy
  // applies its credential Secret, so the running pod is behind until redeploy.
  assert.equal(nextPendingRedeploy(false, withClaude({}), withClaude({ useGateway: false })), true);
  assert.equal(nextPendingRedeploy(false, withClaude({ useGateway: false }), withClaude({})), true);
  assert.equal(nextPendingRedeploy(false, withClaude({}), withClaude({})), false);
});

test('the AI-tab reopen follows the launch the backend resolved', () => {
  // The relaunch decision is the backend's launch command, not a copy of its
  // rules, so a gateway change that moves the launch reopens the tabs and an
  // edit that leaves it unchanged does not.
  const gateway = withClaude({}, 'claude --model vendor/model-x');
  const ownSignIn = withClaude({ useGateway: false }, 'claude --model opus --remote-control');
  assert.notEqual(aiSessionLaunchSignature(gateway), aiSessionLaunchSignature(ownSignIn));
  assert.equal(
    aiSessionLaunchSignature(withClaude({ effort: 'ultracode' }, 'same')),
    aiSessionLaunchSignature(withClaude({}, 'same')),
  );
});
