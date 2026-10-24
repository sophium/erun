import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { UIEnvironmentUsageSnapshot } from '@/uiEnvironmentUsageTypes';

import {
  type EnvironmentUsageMetrics,
  summarizeEnvironmentUsage,
  summarizeEnvironmentUsageMetrics,
} from './environmentUsageSummary';

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
  assert.equal(summary.headline, 'CPU 12.0% · Mem 25% of 2048Mi limit');
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

// Narrows via assert.fail: assert.equal's own narrowing makes a guard a lint tautology.
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
  // A measured zero carries a percent (empty strip); never-sampled carries none.
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
  assert.equal(metrics.memory.suffix, 'of 23.0 GiB limit');
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

test('a measured zero whose percentage the wire dropped still carries a percent', () => {
  // The Go side omits zero percentages (omitempty), so an available metric
  // without one is a measured zero.
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(
      snapshotWith({
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: true, utilization: '0.0%' },
        memory: { available: true, current: '0Mi', limit: '23.0 GiB', oomKills: 0 },
      }),
      Date.now(),
    ),
  );
  assert.equal(metrics.cpu.percent, 0);
  assert.equal(metrics.memory.percent, 0);
  assert.equal(metrics.cpu.value, '0.0%');
  assert.equal(metrics.memory.value, '0%');
});

test('a build-capable environment actively building reports the sidecar, not just the near-idle runtime', () => {
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(
      snapshotWith({
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: true, utilizationPercent: 0.2, utilization: '0.2%' },
        memory: {
          available: true,
          current: '1.1Gi',
          limit: '23.0 GiB',
          percentOfLimit: 2,
          oomKills: 0,
        },
        excludesBuilds: true,
        dind: {
          cpu: { available: true, utilizationPercent: 91.5, utilization: '91.5%', quotaCores: 8 },
          memory: {
            available: true,
            current: '19.4Gi',
            limit: '20.0Gi',
            percentOfLimit: 97,
            oomKills: 0,
          },
        },
      }),
      Date.now(),
    ),
  );
  assert.equal(metrics.cpu.value, '0.2%');
  assert.ok(
    metrics.builds,
    'the sidecar reading must reach the card, not just the runtime container',
  );
  assert.equal(metrics.builds.value, '91.5%');
  assert.equal(metrics.builds.utilization, 91.5);
  assert.equal(metrics.builds.caption, 'erun-dind sidecar · 19.4Gi of 20.0Gi');
});

test('a sidecar with no CPU quota reports cumulative CPU-seconds and no strip', () => {
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(
      snapshotWith({
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: true, utilizationPercent: 0.6, utilization: '0.6%' },
        memory: {
          available: true,
          current: '267Mi',
          limit: '20.0Gi',
          percentOfLimit: 1,
          oomKills: 0,
        },
        excludesBuilds: true,
        dind: {
          cpu: {
            available: false,
            unavailable:
              'cpu.max reports no quota (unlimited or not readable); utilisation needs a quota to measure against',
            usageUsec: 385_919_164,
          },
          memory: { available: true, unlimited: true, current: '512Mi', oomKills: 0 },
        },
      }),
      Date.now(),
    ),
  );
  assert.ok(metrics.builds);
  assert.equal(metrics.builds.value, '386 CPU-s');
  assert.equal(metrics.builds.suffix, 'no quota');
  assert.equal(metrics.builds.utilization, undefined);
  assert.equal(metrics.builds.caption, 'erun-dind sidecar · 512Mi (no limit)');
});

test('an environment that carries no sidecar renders no Builds row', () => {
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(snapshotWith(readable(20)), Date.now()),
  );
  assert.equal(metrics.builds, undefined);
});

test('a build-capable environment whose sidecar could not be read says so rather than showing no Builds row', () => {
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(
      snapshotWith({ ...readable(20), excludesBuilds: true }),
      Date.now(),
    ),
  );
  assert.ok(
    metrics.builds,
    'an unread sidecar is a real state and must render as not-read, not as an environment without one',
  );
  assert.equal(metrics.builds.value, '—');
  assert.equal(metrics.builds.utilization, undefined);
  assert.equal(metrics.builds.caption, 'erun-dind sidecar');
  assert.equal(metrics.builds.note, 'the sidecar could not be read');
});

test('an unreadable sidecar CPU reports the reason instead of an idle zero', () => {
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(
      snapshotWith({
        tenant: 't',
        environment: 'e',
        available: true,
        cpu: { available: true, utilizationPercent: 0.2, utilization: '0.2%' },
        memory: {
          available: true,
          current: '1.1Gi',
          limit: '23.0Gi',
          percentOfLimit: 2,
          oomKills: 0,
        },
        excludesBuilds: true,
        dind: {
          cpu: { available: false, unavailable: 'cpu.stat usage_usec was not readable' },
          memory: { available: false, oomKills: 0 },
        },
      }),
      Date.now(),
    ),
  );
  assert.ok(metrics.builds);
  assert.equal(metrics.builds.value, '—');
  assert.equal(metrics.builds.utilization, undefined);
  assert.equal(metrics.builds.note, 'cpu.stat usage_usec was not readable');
});

// requestReading's container requests far less than its limits.
function requestReading(): UIEnvironmentUsageSnapshot['usage'] {
  const usage = readable(82);
  usage.requests = {
    runtime: { cpuMilli: 250, memoryBytes: 1024 * 1024 * 1024, cpu: '0.25 CPU', memory: '1024Mi' },
  };
  return usage;
}

test('each metric states its ceiling and its reservation', () => {
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(snapshotWith(requestReading()), Date.now()),
  );
  assert.equal(metrics.memory.suffix, 'of 23.0 GiB limit · 1024Mi requested');
  assert.equal(metrics.cpu.suffix, '0.25 CPU requested');
});

test('a missing reservation is absent rather than a requested zero', () => {
  const unread = readingOf(
    summarizeEnvironmentUsageMetrics(snapshotWith(readable(82)), Date.now()),
  );
  assert.equal(unread.memory.suffix, 'of 23.0 GiB limit');
  assert.equal(unread.cpu.suffix, '');
});

test('an unreadable pod spec is reported by the reader, never as zero', () => {
  const usage = readable(82);
  usage.requests = { runtime: {}, unavailable: 'kubectl get pods: connection refused' };
  const metrics = readingOf(summarizeEnvironmentUsageMetrics(snapshotWith(usage), Date.now()));
  assert.equal(metrics.memory.suffix, 'of 23.0 GiB limit');
  assert.equal(metrics.cpu.suffix, '');
});
