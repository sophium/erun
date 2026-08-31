import assert from 'node:assert/strict';
import { test } from 'node:test';

import { orchestratorEnvironmentLine } from './orchestratorEnvironmentActivity';
import type { OrchestratorEnvRef } from './slices/orchestratorsSlice';

function env(overrides: Partial<OrchestratorEnvRef> = {}): OrchestratorEnvRef {
  return {
    tenant: 'petios',
    environment: 'rihards-review',
    directory: '/tmp/dir',
    role: '',
    ...overrides,
  };
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

// The orchestrator card used to reuse the desktop's own reachability reading
// and render it as a confident "Not open here", which read as a claim about
// the *orchestrator's* reach even though the desktop's local forward has
// nothing to do with a host-side orchestrator's own MCP client. The desktop
// genuinely has no reading here (no local forward, no answer from any
// fallback channel either), so the honest line says only what the desktop
// itself observed — never that the environment is closed to anyone else —
// and it says so about "this desktop" explicitly rather than with a bare,
// unscoped "not open".
test('never observed (no activity at all) reads as no signal from this desktop, not a confident "not open"', () => {
  const line = orchestratorEnvironmentLine(env());
  assert.equal(line.state, 'no-forward');
  assert.notEqual(line.state, 'idle');
  assert.notEqual(line.state, 'busy');
  assert.equal(line.status, 'No forward from this desktop');
  assert.notEqual(line.status, 'Not open here');
  assert.ok(!/\bnot open\b/i.test(line.status), 'must not assert a bare "not open"');
  assert.ok(
    /this desktop/i.test(line.status),
    'must scope the claim to the desktop, not the orchestrator',
  );
});

// An orchestrator actively driving a linked environment (a lease held from
// elsewhere, a CLI session, another machine over MCP) must never read as "Not
// open here" — that string used to fire whenever this desktop itself had no
// local forward, which said nothing true about whether the *orchestrator*
// could reach the environment. The poller now asks such an environment
// directly and can still find it busy.
test('a lease held by a session driving the environment from elsewhere still reads busy', () => {
  const line = orchestratorEnvironmentLine(
    env({
      activity: {
        reachable: true,
        observed: true,
        outage: false,
        busy: true,
        detail: 'holding: job-fix-1570',
      },
    }),
  );
  assert.equal(line.state, 'busy');
  assert.equal(line.status, 'Busy — holding: job-fix-1570');
  assert.notEqual(line.status, 'Not open here');
});

test('a failed attempt to reach an unopened environment reads distinctly from never asking', () => {
  const line = orchestratorEnvironmentLine(
    env({
      activity: {
        reachable: false,
        observed: false,
        outage: false,
        checkFailed: true,
        busy: false,
      },
    }),
  );
  assert.equal(line.state, 'check-failed');
  assert.notEqual(line.state, 'no-forward');
  assert.notEqual(line.state, 'idle');
  assert.ok(
    line.status.toLowerCase().includes('open it'),
    'the message should name the recovery action',
  );
});

test('an outage still wins over a failed check, the same way it wins over busy', () => {
  const line = orchestratorEnvironmentLine(
    env({
      activity: { reachable: false, observed: false, outage: true, checkFailed: true, busy: false },
    }),
  );
  assert.equal(line.state, 'outage');
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

// roleLabel makes an operate-role (or any declared-role) link's association
// visible on the hover card — erun#1770's own requirement. Undeclared stays
// silent rather than rendering "Undeclared role" on every pre-existing row.
test('an undeclared role renders no roleLabel', () => {
  const line = orchestratorEnvironmentLine(env({ role: '' }));
  assert.equal(line.roleLabel, '');
});

test('each declared role renders its own caption', () => {
  assert.equal(orchestratorEnvironmentLine(env({ role: 'code' })).roleLabel, 'Code role');
  assert.equal(orchestratorEnvironmentLine(env({ role: 'build' })).roleLabel, 'Build role');
  assert.equal(orchestratorEnvironmentLine(env({ role: 'runtime' })).roleLabel, 'Runtime role');
});

test('the runtime role still renders alongside whatever activity state the environment reports', () => {
  const line = orchestratorEnvironmentLine(
    env({
      role: 'runtime',
      activity: { reachable: true, observed: true, outage: false, busy: false },
    }),
  );
  assert.equal(line.state, 'idle');
  assert.equal(line.roleLabel, 'Runtime role');
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
