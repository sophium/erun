import assert from 'node:assert/strict';
import { test } from 'node:test';

import { summarizeEnvironmentUsage } from './environmentUsageSummary';

// summarizeEnvironmentUsage is what both the env hover card and the
// orchestrator card render from, so its fail-soft contract is what has to be
// right: never a bare 0%/idle-looking figure for "unmeasured", and a stale
// reading always distinguishable from a fresh one.

test('no snapshot at all reads as not yet observed, not idle', () => {
  const summary = summarizeEnvironmentUsage(undefined, Date.now());
  assert.equal(summary.hasReading, false);
  assert.equal(summary.headline, '');
});

test('an unavailable reading states the reason rather than a bare 0', () => {
  const now = Date.now();
  const summary = summarizeEnvironmentUsage(
    {
      usage: {
        tenant: 't',
        environment: 'e',
        available: false,
        message: 'Not running, or not open here: there is no runtime pod to measure.',
        cpu: { available: false },
        memory: { available: false, oomKills: 0 },
      },
      observedAtUnix: Math.floor(now / 1000),
      staleAfterSeconds: 90,
    },
    now,
  );
  assert.equal(summary.headline, '');
  assert.equal(
    summary.detail,
    'Not running, or not open here: there is no runtime pod to measure.',
  );
  assert.equal(summary.hasReading, true);
});

test('an available reading with both cpu and memory renders a compact comparable headline', () => {
  const now = Date.now();
  const summary = summarizeEnvironmentUsage(
    {
      usage: {
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: true, utilization: '12.0%' },
        memory: {
          available: true,
          current: '512Mi',
          limit: '2048Mi',
          percentOfLimit: 25,
          oomKills: 0,
        },
      },
      observedAtUnix: Math.floor(now / 1000),
      staleAfterSeconds: 90,
    },
    now,
  );
  assert.equal(summary.headline, 'CPU 12.0% · Mem 25% of 2048Mi');
  assert.equal(summary.stale, false);
});

test('unlimited memory renders as a real reading, not a failure', () => {
  const now = Date.now();
  const summary = summarizeEnvironmentUsage(
    {
      usage: {
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: false },
        memory: { available: true, unlimited: true, current: '512Mi', oomKills: 0 },
      },
      observedAtUnix: Math.floor(now / 1000),
      staleAfterSeconds: 90,
    },
    now,
  );
  assert.equal(summary.headline, 'Mem 512Mi (no limit)');
});

test('a reading older than staleAfterSeconds is flagged stale', () => {
  const now = Date.now();
  const summary = summarizeEnvironmentUsage(
    {
      usage: {
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: true, utilization: '1.0%' },
        memory: { available: false, oomKills: 0 },
      },
      observedAtUnix: Math.floor(now / 1000) - 200,
      staleAfterSeconds: 90,
    },
    now,
  );
  assert.equal(summary.stale, true);
});

test('a reading within staleAfterSeconds is not flagged stale', () => {
  const now = Date.now();
  const summary = summarizeEnvironmentUsage(
    {
      usage: {
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: true, utilization: '1.0%' },
        memory: { available: false, oomKills: 0 },
      },
      observedAtUnix: Math.floor(now / 1000) - 10,
      staleAfterSeconds: 90,
    },
    now,
  );
  assert.equal(summary.stale, false);
});

test('available but neither cpu nor memory readable states so, not a zero', () => {
  const now = Date.now();
  const summary = summarizeEnvironmentUsage(
    {
      usage: {
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: false },
        memory: { available: false, oomKills: 0 },
      },
      observedAtUnix: Math.floor(now / 1000),
      staleAfterSeconds: 90,
    },
    now,
  );
  assert.equal(summary.headline, '');
  assert.equal(summary.detail, "This environment's own CPU and memory usage could not be read.");
});
