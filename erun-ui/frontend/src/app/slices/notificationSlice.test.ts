import assert from 'node:assert/strict';
import { test } from 'node:test';

import type { AppNotification } from '../state';
import reducer, {
  dismissNotification,
  dismissNotificationForEnv,
  dismissNotifications,
  showNotification,
} from './notificationSlice';

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

test('dismissNotification marks the entry read without removing it from history', () => {
  const state = reducer(undefined, showNotification(notification({ id: 'a' })));

  const next = reducer(state, dismissNotification('a'));

  assert.equal(next.notifications.length, 1, 'the entry must stay in history');
  assert.equal(next.notifications[0]?.dismissed, true);
});

test('dismissNotification is a no-op for an unknown id', () => {
  const state = reducer(undefined, showNotification(notification({ id: 'a' })));

  const next = reducer(state, dismissNotification('does-not-exist'));

  assert.equal(next.notifications[0]?.dismissed, false);
});

test('dismissNotifications scoped to a kind marks only that kind read, leaving others untouched', () => {
  let state = reducer(undefined, showNotification(notification({ id: 'a', kind: 'error' })));
  state = reducer(state, showNotification(notification({ id: 'b', kind: 'warning' })));
  state = reducer(state, showNotification(notification({ id: 'c', kind: 'error' })));

  const next = reducer(state, dismissNotifications('error'));

  assert.deepEqual(
    next.notifications.map((n) => [n.id, n.dismissed]),
    [
      ['a', true],
      ['b', false],
      ['c', true],
    ],
  );
});

test('dismissNotifications with "all" marks every kind read, including debug', () => {
  let state = reducer(undefined, showNotification(notification({ id: 'a', kind: 'error' })));
  state = reducer(state, showNotification(notification({ id: 'b', kind: 'debug' })));

  const next = reducer(state, dismissNotifications('all'));

  assert.deepEqual(
    next.notifications.map((n) => n.dismissed),
    [true, true],
  );
});

test('dismissNotificationForEnv marks every matching entry read, wildcard source included', () => {
  let state = reducer(
    undefined,
    showNotification(
      notification({ id: 'a', tenant: 'frs', environment: 'dev', source: 'runtime-unreachable' }),
    ),
  );
  state = reducer(
    state,
    showNotification(
      notification({ id: 'b', tenant: 'frs', environment: 'dev', source: 'deploy-failed' }),
    ),
  );
  state = reducer(
    state,
    showNotification(
      notification({ id: 'c', tenant: 'frs', environment: 'prod', source: 'runtime-unreachable' }),
    ),
  );

  const next = reducer(
    state,
    dismissNotificationForEnv({ tenant: 'frs', environment: 'dev', source: '' }),
  );

  assert.deepEqual(
    next.notifications.map((n) => [n.id, n.dismissed]),
    [
      ['a', true],
      ['b', true],
      ['c', false],
    ],
  );
});

test('dismissNotificationForEnv with a specific source leaves other sources for the same env alone', () => {
  let state = reducer(
    undefined,
    showNotification(
      notification({ id: 'a', tenant: 'frs', environment: 'dev', source: 'runtime-unreachable' }),
    ),
  );
  state = reducer(
    state,
    showNotification(
      notification({ id: 'b', tenant: 'frs', environment: 'dev', source: 'deploy-failed' }),
    ),
  );

  const next = reducer(
    state,
    dismissNotificationForEnv({ tenant: 'frs', environment: 'dev', source: 'deploy-failed' }),
  );

  assert.deepEqual(
    next.notifications.map((n) => [n.id, n.dismissed]),
    [
      ['a', false],
      ['b', true],
    ],
  );
});

test('history drops the oldest entries once it exceeds capacity', () => {
  let state = reducer(undefined, { type: '@@INIT' });
  for (let i = 0; i < 305; i += 1) {
    state = reducer(state, showNotification(notification({ id: `n-${String(i)}` })));
  }

  assert.equal(state.notifications.length, 300);
  assert.equal(state.notifications[0]?.id, 'n-5', 'the oldest 5 entries must have been dropped');
  assert.equal(state.notifications[state.notifications.length - 1]?.id, 'n-304');
});
