import { test, expect } from '../../../fixtures/erunApp.js';
import { isolatedRoot } from '../../../fixtures/seedRoot.js';
import { isProcessAlive, liveStubProcesses } from '../../../fixtures/stubProcesses.js';

// The desktop, not this process, spawns the stubs, so the harness must reap
// them on spec teardown. Serial: the second case reads what the first parked.
test.describe.configure({ mode: 'serial' });

let stubsTheOpenLeftParked: number[] = [];

test.describe('the stub processes a spec opens do not outlive it', () => {
  test('opening an environment parks a live stub process behind its session', async ({
    app,
    seededEnv,
  }) => {
    test.setTimeout(60_000);
    const before = liveStubProcesses(isolatedRoot());

    // A spec-local env: seeded rows are opened during boot and would be counted.
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);

    await expect
      .poll(() => liveStubProcesses(isolatedRoot()).filter((pid) => !before.includes(pid)).length, {
        timeout: 25_000,
      })
      .toBeGreaterThan(0);
    stubsTheOpenLeftParked = liveStubProcesses(isolatedRoot()).filter(
      (pid) => !before.includes(pid),
    );
  });

  test('none of those stub processes is still alive once that spec has ended', async () => {
    // An empty list means the first case never opened a session, not a pass.
    expect(
      stubsTheOpenLeftParked.length,
      'the case above must open a session first',
    ).toBeGreaterThan(0);

    await expect
      .poll(() => stubsTheOpenLeftParked.filter(isProcessAlive).length, { timeout: 15_000 })
      .toBe(0);
  });
});
