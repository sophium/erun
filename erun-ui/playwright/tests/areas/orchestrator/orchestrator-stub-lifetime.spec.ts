import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';
import { isProcessAlive, liveStubProcesses } from '../../../fixtures/stubProcesses.js';

// An orchestrator row reads "running" only while the session the desktop spawned
// is genuinely up, which is what makes this pair of cases the harness's own
// contract rather than a feature spec:
//
// - the session must be able to come up at all in the gate. A host without a
//   usable `claude` (no TTY, no credentials — the gate image) left every
//   orchestrator spec red in every build, because the real binary exits at once
//   and the dot never turns running (#2481).
// - the stub that makes it come up parks for the life of the session, and the
//   desktop spawns it — not this process — so nothing here collected them: a
//   full run accumulated 117 live stubs, all competing with the suite for the
//   gate's CPUs (#2512).
//
// These assert the process table, not the presence of a stub file on PATH. A
// harness that installs the stub and never reaps it, or that reaps a process
// which never started, fails here; a harness that merely ships the stub passes
// the first case and fails the second.
let stubsTheLivenessCaseOpened: number[] = [];

test.describe('an orchestrator session runs on a stub process the harness owns', () => {
  test('a running orchestrator session is backed by a live stub process', async ({ app }) => {
    // Starting a session is a real state transition whose cost is the backend's,
    // not this assertion's, so the whole-test clock is raised the way the other
    // live-session specs in this area raise it.
    test.setTimeout(60_000);
    const before = liveStubProcesses();

    await app.sidebar.openOrchestratorSession(SEED_ORCHESTRATOR);
    await expect(app.sidebar.orchestratorStatusDot(SEED_ORCHESTRATOR, 'running')).toBeVisible({
      timeout: 25_000,
    });

    // The dot is running *because* a process is parked behind it. Waiting on the
    // registry rather than reading it once lets a stub that registers late still
    // count, without turning the assertion into a race.
    await expect
      .poll(() => liveStubProcesses().filter((pid) => !before.includes(pid)).length, {
        timeout: 15_000,
      })
      .toBeGreaterThan(0);

    stubsTheLivenessCaseOpened = liveStubProcesses().filter((pid) => !before.includes(pid));
  });

  test('the stub that session parked is reaped at the end of its spec', async () => {
    // This file runs in one worker, in declaration order, so the case above has
    // already opened its session. An empty list here means that case did not run
    // — not that nothing leaked — and reporting it as a pass is exactly the
    // vacuous green this pair exists to prevent.
    expect(
      stubsTheLivenessCaseOpened.length,
      'the liveness case above must run first in this worker',
    ).toBeGreaterThan(0);

    // The reap belongs to the app fixture's teardown, which has already run by
    // now. Polling is only for the exit itself, which a signalled stub completes
    // promptly; a stub that survives is the leak, and it stays red here.
    await expect
      .poll(() => stubsTheLivenessCaseOpened.filter(isProcessAlive).length, { timeout: 15_000 })
      .toBe(0);
  });
});
