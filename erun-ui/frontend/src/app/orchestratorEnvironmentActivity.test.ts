import assert from 'node:assert/strict';
import { test } from 'node:test';

import { orchestratorEnvironmentLine } from './orchestratorEnvironmentActivity';
import type { OrchestratorEnvRef } from './slices/orchestratorsSlice';

function env(overrides: Partial<OrchestratorEnvRef> = {}): OrchestratorEnvRef {
  return { tenant: 'petios', environment: 'rihards-review', directory: '/tmp/dir', ...overrides };
}

// This is the join the hover card renders straight from -- red-then-green
// against origin/main means "petios / rihards-review" alone, with nothing
// else on the line. Each case below asserts the state renders as its own
// distinct line rather than collapsing into a neighbour.

test('busy with a detail names the holder verbatim, not a bare "busy"', () => {
  const line = orchestratorEnvironmentLine(
    env({
      activity: {
        reachable: true,
        observed: true,
        outage: false,
        busy: true,
        detail: 'holding: gradle-build',
      },
    }),
  );
  assert.equal(line.state, 'busy');
  assert.equal(line.status, 'Busy — holding: gradle-build');
  assert.equal(line.dot, 'busy');
});

test('busy with no detail still reads busy', () => {
  const line = orchestratorEnvironmentLine(
    env({ activity: { reachable: true, observed: true, outage: false, busy: true } }),
  );
  assert.equal(line.status, 'Busy');
});

test('reachable and observed and not busy reads idle', () => {
  const line = orchestratorEnvironmentLine(
    env({ activity: { reachable: true, observed: true, outage: false, busy: false } }),
  );
  assert.equal(line.state, 'idle');
  assert.equal(line.status, 'Idle');
});

test('reachable but not yet observed does not collapse into idle', () => {
  const line = orchestratorEnvironmentLine(
    env({ activity: { reachable: true, observed: false, outage: false, busy: false } }),
  );
  assert.notEqual(line.state, 'idle');
  assert.equal(line.state, 'unknown');
});

test('an outage reads as outage even while otherwise reachable and busy', () => {
  const line = orchestratorEnvironmentLine(
    env({
      activity: { reachable: true, observed: true, outage: true, busy: true, detail: 'holding: x' },
    }),
  );
  assert.equal(line.state, 'outage');
  assert.notEqual(line.status, 'Idle');
  assert.notEqual(line.status, 'Busy — holding: x');
});

test('never observed (no activity at all) reads unreachable, not idle', () => {
  const line = orchestratorEnvironmentLine(env());
  assert.equal(line.state, 'unreachable');
  assert.notEqual(line.state, 'idle');
});

test('a joined usage reading renders its compact headline on the line', () => {
  const line = orchestratorEnvironmentLine(
    env({
      activity: { reachable: true, observed: true, outage: false, busy: false },
      usage: {
        usage: {
          tenant: 'petios',
          environment: 'rihards-review',
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
        observedAtUnix: Math.floor(Date.now() / 1000),
        staleAfterSeconds: 90,
      },
    }),
  );
  assert.equal(line.usage, 'CPU 12.0% · Mem 25% of 2048Mi');
  assert.equal(line.usageStale, false);
});

test('no joined usage reading yet renders an empty usage line, not a fabricated one', () => {
  const line = orchestratorEnvironmentLine(env());
  assert.equal(line.usage, '');
});

test('a joined usage reading older than its stale window is flagged stale', () => {
  const line = orchestratorEnvironmentLine(
    env({
      activity: { reachable: true, observed: true, outage: false, busy: false },
      usage: {
        usage: {
          tenant: 'petios',
          environment: 'rihards-review',
          available: true,
          cpu: { available: true, utilization: '5.0%' },
          memory: {
            available: true,
            current: '100Mi',
            limit: '2048Mi',
            percentOfLimit: 5,
            oomKills: 0,
          },
        },
        observedAtUnix: Math.floor(Date.now() / 1000) - 300,
        staleAfterSeconds: 90,
      },
    }),
  );
  assert.equal(line.usageStale, true);
});

test('a long environment name and a long detail survive verbatim for the caller to truncate', () => {
  const longName = 'a'.repeat(120);
  const longDetail = 'holding: ' + 'b'.repeat(120);
  const line = orchestratorEnvironmentLine(
    env({
      environment: longName,
      activity: { reachable: true, observed: true, outage: false, busy: true, detail: longDetail },
    }),
  );
  assert.ok(line.name.includes(longName));
  assert.ok(line.status.includes(longDetail));
});
