import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { ActivityQueueEntry } from '../activityQueueState';
import activityReducer, { setActivityEntries, upsertActivityEntry } from './activitySlice';

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

// The state a single entry passes through, as the two writers see it. The
// 'activity:state' stream (upsertActivityEntry) is applied the moment the Go
// store mutates; the ListDeploys snapshot is taken at request time and applied
// later, when the query resolves and its effect runs. Nothing orders the two,
// so a snapshot taken while the entry was still running can be applied after
// the store already finished it -- and the store has no further emit to make,
// because a terminal entry leaves `active` and its container poller exits.
// The card then reads 'running' forever, which puts the sidebar row, the
// launcher badge, and the drawer's "now" section permanently in the wrong
// state. An entry's lastUpdated is written by the store under the same lock
// that produces the snapshot, so it is the ordering both writers can check.
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

  // The event stream lands the finish first.
  const afterFinish = activityReducer(
    { entries: [], locksBySession: {} },
    upsertActivityEntry(finished),
  );
  const [finishedEntry] = afterFinish.entries;
  assert.ok(finishedEntry);
  assert.equal(finishedEntry.status, 'succeeded');

  // Then the already-in-flight snapshot resolves, carrying the older copy.
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

// lastUpdated is stamped by the Go store as RFC3339Nano, so two writes to one
// id can carry sub-millisecond digits; values from the frontend and from
// fixtures carry whole milliseconds. Date.parse keeps only whole milliseconds,
// so comparing the two copies as parsed numbers TIES any pair written inside
// one millisecond -- and a tie hands the win to the payload, which here is the
// stale snapshot. The tie is manufactured by the reader, not by the writers:
// the store's own digits order the pair, and the reduction exists precisely to
// honour that order. Without this, the revert-to-running defect above survives
// intact whenever the snapshot and the finish land in the same millisecond,
// which is exactly the window the two writers race in.
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
  // The premise of the case: as milliseconds these two tie.
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

// The other side of the same rule: sub-millisecond digits must not weld a
// genuinely newer snapshot to an older stream write. A Go stamp with no
// fraction at all ("...:05Z") and one that trims trailing zeros ("...:05.5Z")
// both have to order against a whole-millisecond frontend stamp.
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

  // "...:05Z" is whole milliseconds, "...:05.5Z" is half a second past it, and
  // comparing the raw text gets this backwards -- 'Z' sorts after '.'.
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
