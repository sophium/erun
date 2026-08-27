import assert from 'node:assert/strict';
import { test } from 'node:test';

import { workspaceSyncStatusTone } from './ManageDialog.helpers';

// Before #1418, ManageDialog.fields.tsx's own StatusBadge only distinguished
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
