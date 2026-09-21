import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

import {
  type EnvironmentUsageMetrics,
  summarizeEnvironmentUsage,
  summarizeEnvironmentUsageMetrics,
} from './environmentUsageSummary';

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

// summarizeEnvironmentUsageMetrics feeds the environment card's separate CPU
// and memory rows, each with a decile strip. The property that matters most is
// the one the joined headline could not express: a measured zero and no reading
// at all must not arrive at the renderer the same way.

function snapshotWith(
  usage: UIEnvironmentUsageSnapshot['usage'],
  ageSeconds = 0,
  staleAfterSeconds = 90,
): UIEnvironmentUsageSnapshot {
  return {
    usage,
    observedAtUnix: Math.floor(Date.now() / 1000) - ageSeconds,
    staleAfterSeconds,
  };
}

function readable(percentOfLimit: number): UIEnvironmentUsageSnapshot['usage'] {
  return {
    tenant: 't',
    environment: 'e',
    available: true,
    cpu: { available: true, utilizationPercent: 68.4, utilization: '68.4%' },
    memory: {
      available: true,
      current: '18.9Gi',
      limit: '23.0 GiB',
      percentOfLimit,
      oomKills: 0,
    },
  };
}

// readingOf narrows the result to a reading so the assertions below read as
// plain field access. The narrowing goes through `assert.fail` rather than an
// `assert.equal(kind, 'reading')` first: `assert.equal`'s own signature narrows
// the argument, which makes the follow-up guard a tautology to the linter.
function readingOf(
  metrics: EnvironmentUsageMetrics,
): Extract<EnvironmentUsageMetrics, { kind: 'reading' }> {
  if (metrics.kind !== 'reading') {
    assert.fail('expected a usage reading');
  }
  return metrics;
}

function unreadOf(
  metrics: EnvironmentUsageMetrics,
): Extract<EnvironmentUsageMetrics, { kind: 'unread' }> {
  if (metrics.kind !== 'unread') {
    assert.fail('expected an unread usage result');
  }
  return metrics;
}

test('a never-sampled environment says so rather than rendering an empty strip', () => {
  const metrics = unreadOf(summarizeEnvironmentUsageMetrics(undefined, Date.now()));
  assert.equal(metrics.detail, 'no reading yet');
});

test('a measured zero is a reading, unlike no reading at all', () => {
  // The crossed case: both of these describe "nothing is running", and they must
  // not arrive the same way. A zero carries a percent, so the caller renders an
  // empty (outlined) strip; the never-sampled env carries none, so it renders
  // its degraded text and no strip.
  const zero = readingOf(
    summarizeEnvironmentUsageMetrics(
      snapshotWith({
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: true, utilizationPercent: 0, utilization: '0.0%' },
        memory: {
          available: true,
          current: '0Mi',
          limit: '23.0 GiB',
          percentOfLimit: 0,
          oomKills: 0,
        },
      }),
      Date.now(),
    ),
  );
  assert.equal(zero.cpu.percent, 0);
  assert.equal(zero.memory.percent, 0);

  const never = unreadOf(summarizeEnvironmentUsageMetrics(undefined, Date.now()));
  assert.equal(never.detail, 'no reading yet');
});

test('each metric carries its own figures and percentage', () => {
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(snapshotWith(readable(82)), Date.now()),
  );
  assert.equal(metrics.cpu.label, 'CPU');
  assert.equal(metrics.cpu.value, '68.4%');
  assert.equal(metrics.cpu.percent, 68.4);
  assert.equal(metrics.memory.label, 'Memory');
  assert.equal(metrics.memory.value, '82%');
  assert.equal(metrics.memory.suffix, 'of 23.0 GiB');
  assert.equal(metrics.memory.percent, 82);
});

test('unlimited memory is a real reading with no percentage and so no strip', () => {
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(
      snapshotWith({
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: true, utilizationPercent: 5, utilization: '5.0%' },
        memory: { available: true, unlimited: true, current: '512Mi', oomKills: 0 },
      }),
      Date.now(),
    ),
  );
  assert.equal(metrics.memory.value, '512Mi');
  assert.equal(metrics.memory.suffix, 'no limit');
  assert.equal(metrics.memory.percent, undefined);
});

test('an unreadable metric inside a readable reading states a dash, not a zero', () => {
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(
      snapshotWith({
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: false },
        memory: { available: true, current: '1Gi', limit: '2Gi', percentOfLimit: 50, oomKills: 0 },
      }),
      Date.now(),
    ),
  );
  assert.equal(metrics.cpu.value, '—');
  assert.equal(metrics.cpu.percent, undefined);
  assert.equal(metrics.memory.percent, 50);
});

test('an unreadable-probe reason is reported instead of two empty rows', () => {
  const unavailable = unreadOf(
    summarizeEnvironmentUsageMetrics(
      snapshotWith({
        tenant: 't',
        environment: 'e',
        available: false,
        message: 'Not running, or not open here: there is no runtime pod to measure.',
        cpu: { available: false },
        memory: { available: false, oomKills: 0 },
      }),
      Date.now(),
    ),
  );
  assert.equal(
    unavailable.detail,
    'Not running, or not open here: there is no runtime pod to measure.',
  );

  const neitherReadable = unreadOf(
    summarizeEnvironmentUsageMetrics(
      snapshotWith({
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: false },
        memory: { available: false, oomKills: 0 },
      }),
      Date.now(),
    ),
  );
  assert.equal(
    neitherReadable.detail,
    "This environment's own CPU and memory usage could not be read.",
  );
});

test('a reading older than the sweep interval carries its staleness', () => {
  const stale = readingOf(
    summarizeEnvironmentUsageMetrics(snapshotWith(readable(20), 200, 90), Date.now()),
  );
  assert.equal(stale.stale, true);
  const fresh = readingOf(
    summarizeEnvironmentUsageMetrics(snapshotWith(readable(20), 10, 90), Date.now()),
  );
  assert.equal(fresh.stale, false);
});
