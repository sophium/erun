import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import {
  removeCompletedJob,
  removeHeldLease,
  writeCompletedJob,
  writeHeldLease,
} from '../../../fixtures/seedRoot.js';

const OCCUPANT_LEASE = 'job-fix-1201';

// erun#1221: opening the AI tab on an environment already held by another
// job's activity lease used to silently start a second agent with no
// indication. These specs stage a real lease file (the same on-disk shape
// eruncommon.TakeEnvironmentActivityLease writes) so the headless harness
// drives the actual Go read path, not a mocked RPC.
test.describe('AI tab occupancy notice', () => {
  test('shows who is already here and starts a second agent only on confirmation', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    writeHeldLease(tenant, environment, OCCUPANT_LEASE);

    await app.sidebar.openEnvironment(tenant, environment);

    const dialog = app.aiOccupancyPromptDialog;
    await dialog.waitForOpen();
    await expect(dialog.locator()).toContainText(OCCUPANT_LEASE);

    // The start is pending confirmation — no AI tab yet, and no second agent
    // has been spawned.
    await expect(page.getByRole('tab', { name: 'AI', exact: true })).toHaveCount(0);

    await dialog.startAnyway();
    await dialog.waitForClosed();

    const aiTab = app.tabStrip.tab('AI');
    await app.tabStrip.waitForTab('AI');
    await aiTab.click();

    // Persistent indicator (Nielsen #1: visibility of system status) while the
    // coexisting job is still held — not a one-time toast. The banner renders
    // from the idle-status poll's own answer, so this waits on the poll rather
    // than on expect's 10s default; see the held-poll case below.
    await expect(page.getByText('Another agent is working here')).toBeVisible(withTestBudget());

    removeHeldLease(tenant, environment, OCCUPANT_LEASE);
  });

  test('cancelling leaves no AI tab — starting a second agent stays opt-in', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    writeHeldLease(tenant, environment, OCCUPANT_LEASE);

    await app.sidebar.openEnvironment(tenant, environment);

    const dialog = app.aiOccupancyPromptDialog;
    await dialog.waitForOpen();
    await dialog.cancel();
    await dialog.waitForClosed();

    await expect(page.getByRole('tab', { name: 'AI', exact: true })).toHaveCount(0);

    removeHeldLease(tenant, environment, OCCUPANT_LEASE);
  });

  test('an environment with no held lease shows no occupancy notice', async ({
    app,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);

    await app.tabStrip.waitForTab('AI');
    await expect(app.aiOccupancyPromptDialog.locator()).toHaveCount(0);
  });

  // The banner once named a lease holder and offered "View jobs", but the
  // Jobs tab it routed to reported "No jobs yet" -- the lease's id was only
  // shape-identical to a job's own lease id ("job-<anything>", exactly the
  // CLI's own `--name job-fix-1245` example), with no job record behind it.
  // The banner must not offer an action it cannot substantiate, and must
  // offer it once a real job actually backs the occupancy.
  test('the banner only offers "View jobs" when a real job backs the lease', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    const HAND_LEASE = 'job-visual-demo';
    writeHeldLease(tenant, environment, HAND_LEASE);

    await app.sidebar.openEnvironment(tenant, environment);
    await app.aiOccupancyPromptDialog.waitForOpen();
    await app.aiOccupancyPromptDialog.startAnyway();
    await app.aiOccupancyPromptDialog.waitForClosed();

    const aiTab = page.getByRole('tab', { name: 'AI', exact: true });
    // No cap of its own: a waitFor-family call resolves to the budget the
    // enclosing test declared, and the 20s this used to carry sat below that
    // 30s budget -- it failed a merely slow spawn with the rest of the clock
    // still unspent.
    await aiTab.waitFor({ state: 'visible' });
    await aiTab.click();

    await expect(page.getByText('Another agent is working here')).toBeVisible(withTestBudget());
    const viewJobs = page.getByRole('button', {
      name: `Show the jobs running in ${environment}`,
    });
    await expect(viewJobs).toHaveCount(0);

    removeHeldLease(tenant, environment, HAND_LEASE);
    const JOB_ID = 'gate-9';
    const JOB_LEASE = `job-${JOB_ID}`;
    writeCompletedJob(tenant, environment, JOB_ID, 'repo gate');
    writeHeldLease(tenant, environment, JOB_LEASE);

    // The next idle-status poll (every 1s) picks up the swapped lease. Its
    // budget is this test's own, not the 15s cap that used to sit below it --
    // a poll merely slower than that cap failed a test with half its clock
    // still unspent.
    await expect(viewJobs).toBeVisible(withTestBudget());

    removeHeldLease(tenant, environment, JOB_LEASE);
    removeCompletedJob(tenant, environment, JOB_ID);
  });

  // The banner is rendered from the idle-status poll's own answer, and the
  // assertion that reads it carries no timeout of its own: `toBeVisible` has no
  // way to name one, so it resolves to expect's 10s default while this test
  // declares 60s. A poll that is merely late therefore reds the step with the
  // test's own clock unspent, which is how a loaded machine turns into a
  // failing branch nobody touched.
  //
  // The poll is held past that 10s default -- the smallest delay that
  // discriminates -- so the suite pays seconds here rather than the tens a
  // genuinely loaded machine would. Two details make the reproduction
  // deterministic on a quiet host: the hold is armed only after a poll has
  // already answered, so no request can be in flight when the route goes on,
  // and the gate opens a fixed window after the assertion below begins, so the
  // answer it waits for provably cannot arrive early. Pre-fix this case reds at
  // exactly 10_000ms with 50s of its own budget unused; post-fix it passes at
  // the poll's real arrival.
  test('a lease the idle poll reports past the step cap still reaches the banner', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(60_000);
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    await app.tabStrip.waitForTab('AI');
    await app.tabStrip.tab('AI').click();

    // One poll runs to completion first: the app arms the next one a second
    // after its response, so installing the route here covers a request that
    // cannot have been sent before it -- and therefore cannot carry the lease
    // staged below.
    await page.waitForResponse(
      (response) =>
        response.url().includes('/__erun_invoke') &&
        (response.request().postData() ?? '').includes('LoadIdleStatus'),
    );

    let releaseIdlePoll: () => void = () => undefined;
    const idlePollHeld = new Promise<void>((resolve) => {
      releaseIdlePoll = resolve;
    });
    let held = false;
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
      if (body.method === 'LoadIdleStatus' && !held) {
        held = true;
        await idlePollHeld;
      }
      await route.continue();
    });

    writeHeldLease(tenant, environment, OCCUPANT_LEASE);

    // Deliberate stimulus, not a wait for the app: the hold *is* the contention
    // this case exists to reproduce, so it is sized on the clock it has to
    // disagree with.
    setTimeout(releaseIdlePoll, 15_000);
    await expect(page.getByText('Another agent is working here')).toBeVisible(withTestBudget());

    removeHeldLease(tenant, environment, OCCUPANT_LEASE);
  });
});
