import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { ActivityQueueEntry } from '@/app/activityQueueState';

import { activityElapsedLabel } from './ActivityQueueDrawer.helpers';

function entry(overrides: Partial<ActivityQueueEntry>): ActivityQueueEntry {
  return {
    id: 'entry-1',
    command: 'deploy',
    tenant: 'acme',
    environment: 'prod',
    status: 'running',
    startedAt: '2026-08-24T00:00:00.000Z',
    lastUpdated: '2026-08-24T00:00:00.000Z',
    ...overrides,
  };
}

test('a running entry that waited before starting anchors elapsed on startedRunningAt', () => {
  const queued = entry({
    status: 'running',
    enqueuedAt: '2026-08-24T00:00:00.000Z',
    startedAt: '2026-08-24T00:00:00.000Z',
    startedRunningAt: '2026-08-24T00:06:40.000Z',
  });
  const now = Date.parse('2026-08-24T00:06:50.000Z');
  assert.equal(activityElapsedLabel(queued, now), '   10s');
});

test('a running entry with no queue wait falls back to startedAt', () => {
  const immediate = entry({
    status: 'running',
    startedAt: '2026-08-24T00:00:00.000Z',
  });
  const now = Date.parse('2026-08-24T00:00:05.000Z');
  assert.equal(activityElapsedLabel(immediate, now), '    5s');
});

test('a waiting entry still anchors on enqueuedAt', () => {
  const waiting = entry({
    status: 'waiting',
    enqueuedAt: '2026-08-24T00:00:00.000Z',
    startedAt: '2026-08-24T00:00:00.000Z',
  });
  const now = Date.parse('2026-08-24T00:00:03.000Z');
  assert.equal(activityElapsedLabel(waiting, now), '    3s');
});
