import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  DIALOG_FILTER_KINDS,
  filterNotificationHistory,
  notificationHistoryNewestFirst,
  notificationIdentityLabel,
  notificationKindLabel,
  notificationKindTone,
  TITLEBAR_ICON_KINDS,
  totalUnreadCount,
  unreadNotificationCounts,
} from './notificationCenter';
import type { AppNotification } from './state';

function notification(overrides: Partial<AppNotification>): AppNotification {
  return {
    id: 'n-1',
    kind: 'info',
    message: 'hello',
    timestamp: 0,
    dismissed: false,
    ...overrides,
  };
}

test('unreadNotificationCounts counts only not-yet-dismissed entries per kind', () => {
  const counts = unreadNotificationCounts([
    notification({ id: '1', kind: 'error', dismissed: false }),
    notification({ id: '2', kind: 'error', dismissed: true }),
    notification({ id: '3', kind: 'warning', dismissed: false }),
    notification({ id: '4', kind: 'warning', dismissed: false }),
    notification({ id: '5', kind: 'debug', dismissed: false }),
  ]);
  assert.deepEqual(counts, { error: 1, warning: 2, info: 0, success: 0, debug: 1 });
});

test('totalUnreadCount sums every class, including debug', () => {
  const counts = unreadNotificationCounts([
    notification({ id: '1', kind: 'error', dismissed: false }),
    notification({ id: '2', kind: 'warning', dismissed: false }),
    notification({ id: '3', kind: 'debug', dismissed: false }),
    notification({ id: '4', kind: 'error', dismissed: true }),
  ]);
  assert.equal(totalUnreadCount(counts), 3);
});

test('TITLEBAR_ICON_KINDS excludes only debug', () => {
  assert.deepEqual([...TITLEBAR_ICON_KINDS].sort(), ['error', 'info', 'success', 'warning']);
});

test('DIALOG_FILTER_KINDS includes every kind, including debug', () => {
  assert.deepEqual([...DIALOG_FILTER_KINDS].sort(), [
    'debug',
    'error',
    'info',
    'success',
    'warning',
  ]);
});

test('notificationHistoryNewestFirst sorts by timestamp descending without mutating the input', () => {
  const oldest = notification({ id: 'a', timestamp: 1 });
  const newest = notification({ id: 'b', timestamp: 3 });
  const middle = notification({ id: 'c', timestamp: 2 });
  const input = [oldest, newest, middle];

  const sorted = notificationHistoryNewestFirst(input);

  assert.deepEqual(
    sorted.map((n) => n.id),
    ['b', 'c', 'a'],
  );
  assert.deepEqual(
    input.map((n) => n.id),
    ['a', 'b', 'c'],
    'the input array must not be mutated',
  );
});

test('filterNotificationHistory hides debug entries by default under every filter, including "all"', () => {
  const entries = [
    notification({ id: '1', kind: 'debug' }),
    notification({ id: '2', kind: 'info' }),
  ];
  assert.deepEqual(
    filterNotificationHistory(entries, 'all', false).map((n) => n.id),
    ['2'],
  );
  assert.deepEqual(filterNotificationHistory(entries, 'debug', false), []);
});

test('filterNotificationHistory reveals debug entries once the toggle is on', () => {
  const entries = [
    notification({ id: '1', kind: 'debug' }),
    notification({ id: '2', kind: 'info' }),
  ];
  assert.deepEqual(
    filterNotificationHistory(entries, 'all', true).map((n) => n.id),
    ['1', '2'],
  );
  assert.deepEqual(
    filterNotificationHistory(entries, 'debug', true).map((n) => n.id),
    ['1'],
  );
});

test('filterNotificationHistory narrows to a single class', () => {
  const entries = [
    notification({ id: '1', kind: 'error' }),
    notification({ id: '2', kind: 'warning' }),
  ];
  assert.deepEqual(
    filterNotificationHistory(entries, 'error', false).map((n) => n.id),
    ['1'],
  );
});

test('notificationIdentityLabel prefers tenant/environment over an orchestrator id', () => {
  assert.equal(
    notificationIdentityLabel(notification({ tenant: 'frs', environment: 'dev' })),
    'frs / dev',
  );
});

test('notificationIdentityLabel falls back to the orchestrator id', () => {
  assert.equal(
    notificationIdentityLabel(notification({ orchestratorId: 'orc-1' })),
    'Orchestrator orc-1',
  );
});

test('notificationIdentityLabel is null for an untagged one-shot toast', () => {
  assert.equal(notificationIdentityLabel(notification({})), null);
});

test('notificationKindTone/notificationKindLabel cover every kind', () => {
  for (const kind of [...DIALOG_FILTER_KINDS, 'success' as const]) {
    assert.ok(notificationKindTone(kind));
    assert.ok(notificationKindLabel(kind));
  }
});
