import assert from 'node:assert/strict';

import { test } from 'vitest';

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

// The defect these pin: a build saturating its cap runs in the erun-dind
// sidecar, so the runtime container's CPU figure beside it reads near zero. A
// headline without the build's own number tells an operator the environment is
// idle at the exact moment it is at its ceiling -- and the answer to "is the
// build running?" would be nowhere on the card.
test('a saturated build cgroup renders its own figure beside the idle container CPU', () => {
  const now = Date.now();
  const summary = summarizeEnvironmentUsage(
    {
      usage: {
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: true, utilization: '0.2%' },
        memory: { available: true, current: '512Mi', limit: '2048Mi', percentOfLimit: 25, oomKills: 0 },
        build: {
          available: true,
          quota: '4.00 cores',
          utilization: '100.0%',
          periods: 200,
          throttledPeriods: 200,
        },
      },
      observedAtUnix: Math.floor(now / 1000),
      staleAfterSeconds: 90,
    },
    now,
  );
  assert.equal(
    summary.headline,
    'CPU 0.2% · Mem 25% of 2048Mi · Build 100.0% of 4.00 cores (throttled 200/200)',
  );
});

test('an unreadable build cgroup adds no figure rather than a zero', () => {
  const now = Date.now();
  const summary = summarizeEnvironmentUsage(
    {
      usage: {
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: true, utilization: '0.2%' },
        memory: { available: false, oomKills: 0 },
        build: { available: false, unavailable: 'the erun-dind sidecar has no build cgroup' },
      },
      observedAtUnix: Math.floor(now / 1000),
      staleAfterSeconds: 90,
    },
    now,
  );
  assert.equal(summary.headline, 'CPU 0.2%');
});

test('an environment with no build cgroup at all renders as before', () => {
  const now = Date.now();
  const summary = summarizeEnvironmentUsage(
    {
      usage: {
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: true, utilization: '0.2%' },
        memory: { available: false, oomKills: 0 },
      },
      observedAtUnix: Math.floor(now / 1000),
      staleAfterSeconds: 90,
    },
    now,
  );
  assert.equal(summary.headline, 'CPU 0.2%');
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
