import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UIEnvironmentConfig } from '@/types';

import {
  aiSessionLaunchSignature,
  claudeRoutingChanged,
  compactClaudeDraft,
  manageDialogTabHasUnsavedChanges,
  nextPendingRedeploy,
} from './manageDialogHelpers';

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
  // The dirty check compares serialized drafts, so key order must match the backend.
  const initial = withClaude({ useBedrock: true, useGateway: false, models: ['opus'] });
  const reEdited = withClaude(compactClaudeDraft({ ...initial.claude, effort: undefined }));
  assert.equal(manageDialogTabHasUnsavedChanges('ai', reEdited, initial), false);
});

test('a gateway change raises the pending-redeploy banner', () => {
  assert.equal(nextPendingRedeploy(false, withClaude({}), withClaude({ useGateway: false })), true);
  assert.equal(nextPendingRedeploy(false, withClaude({ useGateway: false }), withClaude({})), true);
  assert.equal(nextPendingRedeploy(false, withClaude({}), withClaude({})), false);
});

test('the AI-tab reopen follows the launch the backend resolved', () => {
  const gateway = withClaude({}, 'claude --model vendor/model-x');
  const ownSignIn = withClaude({ useGateway: false }, 'claude --model opus --remote-control');
  assert.notEqual(aiSessionLaunchSignature(gateway), aiSessionLaunchSignature(ownSignIn));
  assert.equal(
    aiSessionLaunchSignature(withClaude({ effort: 'ultracode' }, 'same')),
    aiSessionLaunchSignature(withClaude({}, 'same')),
  );
});

test('a save that moves the Claude routing leaves the AI-tab reopen to the redeploy', () => {
  assert.equal(claudeRoutingChanged(withClaude({}), withClaude({ useGateway: false })), true);
  assert.equal(claudeRoutingChanged(withClaude({}), withClaude({ useBedrock: true })), true);
  assert.equal(claudeRoutingChanged(withClaude({}), withClaude({ effort: 'low' })), false);
});
