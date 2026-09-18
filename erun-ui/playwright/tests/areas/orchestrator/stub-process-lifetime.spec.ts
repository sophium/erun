import { test, expect } from '../../../fixtures/erunApp.js';
import { isProcessAlive, liveStubProcesses } from '../../../fixtures/stubProcesses.js';

// The harness's long-lived stubs park for the life of the session they stand in
// for — an orchestrator row reads "running" only while its process is up, so a
// stub that exits is a spec that times out. The desktop spawns them, though, and
// not this process, so nothing here collects one: a spec that opened a session
// and never closed it left a live `sleep` behind for the rest of the run, and a
// full ALL run accumulated 117 of them, all competing with the suite for the
// gate's CPUs (#2512).
//
// So the harness reaps the stubs a spec opened on that spec's teardown
// (fixtures/stubProcesses.ts, wired as an automatic fixture). These cases assert
// that on the process table: a session this spec opens must be backed by a real
// live process, and that process must be gone by the time the next spec starts.
// A harness that installs the stub and never reaps it passes the first case and
// fails the second.
//
// Serial because the second case reads what the first one parked, and this suite
// runs fully parallel — without it the two are handed to different workers and
// the second asserts against an empty box.
test.describe.configure({ mode: 'serial' });

let stubsTheOpenLeftParked: number[] = [];

test.describe('the stub processes a spec opens do not outlive it', () => {
  test('opening an environment parks a live stub process behind its session', async ({
    app,
    seededEnv,
  }) => {
    // Opening a session costs the backend, not this assertion, so the whole-test
    // clock is raised the way the other session-opening specs raise it.
    test.setTimeout(60_000);
    const before = liveStubProcesses();

    // A spec-local environment: the seeded baseline's own rows are opened for it
    // during boot, so counting those would measure the boot rather than this
    // spec. Anything parked before this click is excluded by the diff below.
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);

    await expect
      .poll(() => liveStubProcesses().filter((pid) => !before.includes(pid)).length, {
        timeout: 25_000,
      })
      .toBeGreaterThan(0);
    stubsTheOpenLeftParked = liveStubProcesses().filter((pid) => !before.includes(pid));
  });

  test('none of those stub processes is still alive once that spec has ended', async () => {
    // The first case runs before this one (serial), so an empty list here means
    // it never opened a session — not that nothing leaked — and reporting that
    // as a pass is exactly the vacuous green this pair exists to prevent.
    expect(
      stubsTheOpenLeftParked.length,
      'the case above must open a session first',
    ).toBeGreaterThan(0);

    // The reap belongs to the spec fixture's teardown, which has already run by
    // now. Polling is only for the exit itself, which a signalled stub completes
    // promptly; a stub that survives is the leak, and it stays red here.
    await expect
      .poll(() => stubsTheOpenLeftParked.filter(isProcessAlive).length, { timeout: 15_000 })
      .toBe(0);
  });
});
