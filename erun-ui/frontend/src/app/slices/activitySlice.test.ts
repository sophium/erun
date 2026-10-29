import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { ActivityQueueEntry } from '../activityQueueState';
import activityReducer, { setActivityEntries, upsertActivityEntry } from './activitySlice';

// ListDeploys never returns the synthetic 'invite-approval' entry, which holds
// the only copy of the accept-invite link.

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
  const resynced = activityReducer(
    state,
    setActivityEntries([inviteApprovalEntry({ message: 'updated' })]),
  );
  assert.equal(resynced.entries.length, 1);
  assert.equal(resynced.entries[0]?.message, 'updated');
});

// A ListDeploys snapshot can land after the stream already finished the entry;
// lastUpdated, stamped under the store's lock, orders the two.
test('a ListDeploys snapshot taken before an entry finished cannot revert it to running', () => {
  const running = clusterEntry({
    id: 'deploy-1',
    status: 'running',
    lastUpdated: '2026-08-24T00:00:00.000Z',
  });
  const finished = clusterEntry({
    id: 'deploy-1',
    status: 'succeeded',
    lastUpdated: '2026-08-24T00:00:05.000Z',
    endedAt: '2026-08-24T00:00:05.000Z',
  });

  const afterFinish = activityReducer(
    { entries: [], locksBySession: {} },
    upsertActivityEntry(finished),
  );
  const [finishedEntry] = afterFinish.entries;
  assert.ok(finishedEntry);
  assert.equal(finishedEntry.status, 'succeeded');

  const resynced = activityReducer(afterFinish, setActivityEntries([running]));
  const [resyncedEntry] = resynced.entries;
  assert.ok(resyncedEntry, 'expected the entry to survive the resync');
  assert.equal(
    resyncedEntry.status,
    'succeeded',
    'the older snapshot must not overwrite the newer state',
  );
  assert.equal(resyncedEntry.endedAt, '2026-08-24T00:00:05.000Z');
});

// Date.parse floors to milliseconds, which would tie RFC3339Nano stamps and
// hand the win to the stale snapshot.
test('a snapshot and a finish stamped inside one millisecond still order by the store digits', () => {
  const running = clusterEntry({
    id: 'deploy-1',
    status: 'running',
    lastUpdated: '2026-08-24T00:00:05.000100000Z',
  });
  const finished = clusterEntry({
    id: 'deploy-1',
    status: 'succeeded',
    lastUpdated: '2026-08-24T00:00:05.000900000Z',
    endedAt: '2026-08-24T00:00:05.000900000Z',
  });
  assert.equal(
    Date.parse(running.lastUpdated),
    Date.parse(finished.lastUpdated),
    'expected the two stamps to tie once truncated to milliseconds',
  );

  const afterFinish = activityReducer(
    { entries: [], locksBySession: {} },
    upsertActivityEntry(finished),
  );
  const resynced = activityReducer(afterFinish, setActivityEntries([running]));
  const [resyncedEntry] = resynced.entries;
  assert.ok(resyncedEntry, 'expected the entry to survive the resync');
  assert.equal(
    resyncedEntry.status,
    'succeeded',
    'a same-millisecond snapshot must not overwrite the finish the store ordered after it',
  );
  assert.equal(resyncedEntry.endedAt, '2026-08-24T00:00:05.000900000Z');
});

test('a snapshot stamped later inside the same millisecond still carries its update', () => {
  const running = clusterEntry({
    id: 'deploy-1',
    status: 'running',
    lastUpdated: '2026-08-24T00:00:05.000100000Z',
  });
  const progressed = clusterEntry({
    id: 'deploy-1',
    status: 'running',
    lastUpdated: '2026-08-24T00:00:05.000900000Z',
    containers: [
      { name: 'runtime', image: 'erun-runtime:1', phase: 'Running', ready: true, restarts: 0 },
    ],
  });
  const resynced = activityReducer(
    activityReducer({ entries: [], locksBySession: {} }, upsertActivityEntry(running)),
    setActivityEntries([progressed]),
  );
  const [resyncedEntry] = resynced.entries;
  assert.ok(resyncedEntry);
  assert.equal(resyncedEntry.containers?.length, 1);
  assert.equal(resyncedEntry.lastUpdated, '2026-08-24T00:00:05.000900000Z');
});

test('a fractionless store stamp and a trimmed-fraction store stamp order correctly', () => {
  const wholeSecond = clusterEntry({
    id: 'deploy-1',
    status: 'running',
    lastUpdated: '2026-08-24T00:00:05Z',
  });
  const trimmed = clusterEntry({
    id: 'deploy-1',
    status: 'succeeded',
    lastUpdated: '2026-08-24T00:00:05.5Z',
    endedAt: '2026-08-24T00:00:05.5Z',
  });

  // Raw text comparison gets this backwards: 'Z' sorts after '.'.
  const resynced = activityReducer(
    activityReducer({ entries: [], locksBySession: {} }, upsertActivityEntry(trimmed)),
    setActivityEntries([wholeSecond]),
  );
  const [resyncedEntry] = resynced.entries;
  assert.ok(resyncedEntry);
  assert.equal(
    resyncedEntry.status,
    'succeeded',
    'a fractionless stamp is earlier than a fractioned one in the same second',
  );
});

test('a ListDeploys snapshot still carries a genuine update of a running entry', () => {
  const running = clusterEntry({
    id: 'deploy-1',
    status: 'running',
    lastUpdated: '2026-08-24T00:00:00.000Z',
  });
  const progressed = clusterEntry({
    id: 'deploy-1',
    status: 'running',
    lastUpdated: '2026-08-24T00:00:09.000Z',
    containers: [
      { name: 'runtime', image: 'erun-runtime:1', phase: 'Running', ready: true, restarts: 0 },
    ],
  });

  const resynced = activityReducer(
    activityReducer({ entries: [], locksBySession: {} }, upsertActivityEntry(running)),
    setActivityEntries([progressed]),
  );
  const [resyncedEntry] = resynced.entries;
  assert.ok(resyncedEntry);
  assert.equal(resyncedEntry.containers?.length, 1);
  assert.equal(resyncedEntry.lastUpdated, '2026-08-24T00:00:09.000Z');
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
