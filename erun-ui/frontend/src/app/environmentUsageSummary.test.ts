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
  // The Go side marks these percentage fields `omitempty` and sets
  // `utilization` unconditionally on every available CPU reading, so a genuine
  // 0% arrives as `utilization: '0.0%'` with NO `utilizationPercent` key, and a
  // 0-of-limit memory reading with no `percentOfLimit`. Reading a missing
  // percentage as "unmeasured" would give the two states the same treatment --
  // no strip for either -- which is the idle/unmeasured confusion the strip
  // exists to remove. An available metric with no percentage is a measured zero.
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
  // And the figure itself is a zero, not the dash reserved for unmeasured.
  assert.equal(metrics.cpu.value, '0.0%');
  assert.equal(metrics.memory.value, '0%');
});

// The Builds row is the erun-dind sidecar's reading, and it exists because the
// runtime container's own CPU cannot answer "is this build working": a release
// lane spends its time waiting on bounded `erun exec job await` calls, so that
// figure is near zero whether the build is healthy or wedged. The report was
// exactly that -- an environment busy holding a release, reading 0.2% CPU.
test('a build-capable environment actively building reports the sidecar, not just the near-idle runtime', () => {
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(
      snapshotWith({
        tenant: 't',
        environment: 'e',
        available: true,
        // The runtime container during an active build: near-idle by
        // construction, and the only figure the card used to draw.
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

// A sidecar that declares no cpu.max quota cannot report a percentage, and
// reporting nothing would put the operator back where they started: a
// build-capable environment whose only visible CPU figure is the runtime
// container's near-zero. It has done measurable work, and CPU-seconds is how a
// container with no ceiling states it -- with no strip, because there is no
// ceiling to be a fraction of.
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
  // Memory with no ceiling is still a real reading, stated without a limit.
  assert.equal(metrics.builds.caption, 'erun-dind sidecar · 512Mi (no limit)');
});

// An absent `dind` has two causes, and this is the one that is not a defect:
// the environment carries no erun-dind sidecar, so there is nothing to report
// and no Builds row to draw. Rendering the not-read row below here would put a
// permanent dash on every runtime environment.
test('an environment that carries no sidecar renders no Builds row', () => {
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(snapshotWith(readable(20)), Date.now()),
  );
  assert.equal(metrics.builds, undefined);
});

// The reported defect, and the other half of the distinction above. An absent
// `dind` is not a zero, but it is also not one state: on an environment that
// carries the sidecar it means the exec into it failed (an older runtime image,
// a sidecar mid-restart), and the card rendered that exactly like an
// environment carrying no sidecar at all -- as nothing. The operator was left
// with the runtime container's near-idle CPU and memory qualified only by the
// age caption's "excludes builds" caveat, which reads as idle rather than as
// unknown, beside an Activity line saying a build was running. `excludesBuilds`
// is the field that tells the two apart, and the unread state must render where
// the Builds row would have been -- never as a zero.
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

// requestReading is a reading whose pod spec was read: the runtime container
// declares the chart's fixed 0.25 CPU / 1.0GiB against limits in the tens of
// GiB, which is exactly the state the card used to render as provisioned.
function requestReading(): UIEnvironmentUsageSnapshot['usage'] {
  const usage = readable(82);
  usage.requests = {
    runtime: { cpuMilli: 250, memoryBytes: 1024 * 1024 * 1024, cpu: '0.25 CPU', memory: '1024Mi' },
  };
  return usage;
}

// TestEachMetricStatesItsCeilingAndItsReservation is the card half of the
// reservation reading: `82% of 23.0 GiB` reads as an environment holding 23.0
// GiB, when the container reserves 1.0GiB and the rest is only what it may
// grow to under pressure. Naming the ceiling a limit and stating the request
// beside it is what makes the two distinguishable without arithmetic.
test('each metric states its ceiling and its reservation', () => {
  const metrics = readingOf(
    summarizeEnvironmentUsageMetrics(snapshotWith(requestReading()), Date.now()),
  );
  assert.equal(metrics.memory.suffix, 'of 23.0 GiB limit · 1024Mi requested');
  assert.equal(metrics.cpu.suffix, '0.25 CPU requested');
});

// TestAMissingReservationIsAbsentRatherThanZero covers the state a fabricated
// zero would misstate: an unread pod spec (an older cluster, kubectl refusing
// the read) leaves the reservation unstated, while the limits and the usage
// figures beside it keep rendering. "Could not read the reservation" is not
// "reserves nothing", and only one of them is true here.
test('a missing reservation is absent rather than a requested zero', () => {
  const unread = readingOf(
    summarizeEnvironmentUsageMetrics(snapshotWith(readable(82)), Date.now()),
  );
  assert.equal(unread.memory.suffix, 'of 23.0 GiB limit');
  assert.equal(unread.cpu.suffix, '');
});

// TestAnUnreadablePodSpecIsStatedRatherThanSilent is the other half of that
// distinction: a read that was attempted and failed has a reason to give, and
// the card's own honesty contract (never a number nobody measured) is what the
// reason preserves.
test('an unreadable pod spec is reported by the reader, never as zero', () => {
  const usage = readable(82);
  usage.requests = { runtime: {}, unavailable: 'kubectl get pods: connection refused' };
  const metrics = readingOf(summarizeEnvironmentUsageMetrics(snapshotWith(usage), Date.now()));
  assert.equal(metrics.memory.suffix, 'of 23.0 GiB limit');
  assert.equal(metrics.cpu.suffix, '');
});
