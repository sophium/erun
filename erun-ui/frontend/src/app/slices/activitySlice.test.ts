import assert from 'node:assert/strict';
import { test } from 'node:test';

import type { ActivityQueueEntry } from '../activityQueueState';
import activityReducer, { setActivityEntries } from './activitySlice';

// setActivityEntries syncs the drawer with ListDeploys's cluster/host-observed
// list. A synthetic 'invite-approval' entry (pushInviteApprovalActivityEntry)
// carries the only copy of an accept-invite link, and ListDeploys never
// returns one -- so a plain replace on every refetch (triggered by dismissing
// any unrelated entry elsewhere in the drawer) would silently wipe it out
// from underneath the operator before they copy the link.

function clusterEntry(overrides: Partial<ActivityQueueEntry>): ActivityQueueEntry {
  return {
    id: 'deploy-1',
    command: 'deploy',
    tenant: 'acme',
    environment: 'prod',
    status: 'running',
    startedAt: '2026-08-24T00:00:00.000Z',
    lastUpdated: '2026-08-24T00:00:00.000Z',
    ...overrides,
  };
}

function inviteApprovalEntry(overrides: Partial<ActivityQueueEntry> = {}): ActivityQueueEntry {
  return {
    id: 'invite-approval-acme-2026-08-24T00:00:00.000Z',
    command: 'invite-approval',
    tenant: 'acme',
    environment: '',
    status: 'succeeded',
    startedAt: '2026-08-24T00:00:00.000Z',
    lastUpdated: '2026-08-24T00:00:00.000Z',
    origin: 'invite-approval',
    message: "Approved -- you're enrolled in acme.",
    inviteLink: 'https://console.example.test/accept-invite?token=tok-abc',
    ...overrides,
  };
}

test('a fresh ListDeploys sync preserves an existing invite-approval entry', () => {
  const withInvite = activityReducer(
    { entries: [], locksBySession: {} },
    setActivityEntries([inviteApprovalEntry()]),
  );
  assert.equal(withInvite.entries.length, 1);

  // Simulate the refetch a dismiss of some unrelated deploy entry triggers:
  // ListDeploys comes back with only the cluster-observed entries, never the
  // synthetic one.
  const resynced = activityReducer(
    withInvite,
    setActivityEntries([clusterEntry({ id: 'deploy-1' })]),
  );

  assert.equal(resynced.entries.length, 2);
  const invite = resynced.entries.find((e) => e.origin === 'invite-approval');
  assert.ok(invite, 'expected the invite-approval entry to survive the resync');
  assert.equal(invite.inviteLink, 'https://console.example.test/accept-invite?token=tok-abc');
});

test('an invite-approval entry already present in the fresh payload is not duplicated', () => {
  const state = activityReducer(
    { entries: [], locksBySession: {} },
    setActivityEntries([inviteApprovalEntry()]),
  );
  // however unlikely, if ListDeploys ever did carry the same id back, the
  // fresh copy wins rather than stacking a duplicate row.
  const resynced = activityReducer(
    state,
    setActivityEntries([inviteApprovalEntry({ message: 'updated' })]),
  );
  assert.equal(resynced.entries.length, 1);
  assert.equal(resynced.entries[0]?.message, 'updated');
});

test('an explicitly dismissed invite-approval entry does not come back on the next resync', () => {
  const withInvite = activityReducer(
    { entries: [], locksBySession: {} },
    setActivityEntries([inviteApprovalEntry()]),
  );
  const dismissed = activityReducer(withInvite, {
    type: 'activity/removeActivityEntry',
    payload: inviteApprovalEntry().id,
  });
  assert.equal(dismissed.entries.length, 0);

  const resynced = activityReducer(
    dismissed,
    setActivityEntries([clusterEntry({ id: 'deploy-1' })]),
  );
  assert.equal(resynced.entries.length, 1);
  assert.equal(resynced.entries[0]?.id, 'deploy-1');
});
