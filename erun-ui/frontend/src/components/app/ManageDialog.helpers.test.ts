import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UIEnvironmentConfig } from '@/types';

import { effectiveClaudeModelDefaults, workspaceSyncStatusTone } from './ManageDialog.helpers';

// ManageDialog.fields.tsx's own StatusBadge used to only distinguish
// 'running' (green) and 'stopped' (muted); every other workspaceSyncStatus
// value -- including 'starting' and 'syncing', which are in-progress rather
// than failed -- fell into its catch-all destructive (red) branch. The
// canonical erun-kit StatusBadge this now renders through needs a tone for
// every value workspace_sync.go actually sets.
test('a sync in progress reads as in-progress, not destructive', () => {
  assert.equal(workspaceSyncStatusTone('starting'), 'in-progress');
  assert.equal(workspaceSyncStatusTone('syncing'), 'in-progress');
});

test('a sync failure reads as destructive', () => {
  assert.equal(workspaceSyncStatusTone('error'), 'destructive');
});

test('a stopped sync reads as muted, not destructive', () => {
  assert.equal(workspaceSyncStatusTone('stopped'), 'muted');
});

test('an unrecognized status falls back to warning rather than defaulting to destructive', () => {
  assert.equal(workspaceSyncStatusTone('mystery'), 'warning');
});

// The AI tab's model choices follow the draft's gateway choice: an environment
// that leaves the gateway is offered its own models, not the catalog's ids its
// own sign-in cannot launch, and one on the gateway is offered the catalog.
const gatewayDefaults = {
  models: ['opus', 'sonnet', 'haiku'],
  knownModels: ['opus', 'sonnet', 'haiku', 'fable'],
  gatewayConfigured: true,
  gatewayModels: ['vendor/model-x'],
  gatewayDefaultModel: 'vendor/model-x',
} as UIEnvironmentConfig['claudeDefaults'];

test('an environment on the gateway is offered the catalog', () => {
  const effective = effectiveClaudeModelDefaults({}, gatewayDefaults);
  assert.equal(effective.viaGateway, true);
  assert.deepEqual(effective.available, ['vendor/model-x']);
  assert.equal(effective.defaultLaunchModel, 'vendor/model-x');
});

test('an environment that left the gateway is offered its own models', () => {
  const effective = effectiveClaudeModelDefaults({ useGateway: false }, gatewayDefaults);
  assert.equal(effective.viaGateway, false);
  assert.deepEqual(effective.available, ['opus', 'sonnet', 'haiku']);
  assert.equal(effective.defaultLaunchModel, 'opus');
});

test('without a catalog the environment keeps its own models', () => {
  const effective = effectiveClaudeModelDefaults(
    { models: ['sonnet'] },
    { ...gatewayDefaults, gatewayConfigured: false },
  );
  assert.equal(effective.viaGateway, false);
  assert.deepEqual(effective.available, ['sonnet']);
});
