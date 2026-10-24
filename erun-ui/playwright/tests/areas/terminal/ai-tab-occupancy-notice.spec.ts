import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import {
  removeActivitySnapshot,
  removeCompletedJob,
  removeHeldLease,
  writeCompletedJob,
  writeHeldLease,
  writeTruncatedActivitySnapshot,
} from '../../../fixtures/seedRoot.js';

const OCCUPANT_LEASE = 'job-fix-1201';

// Opening AI on an env held by another lease must not silently start a second
// agent. A real lease file is staged so the Go read path runs.
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

    await expect(page.getByRole('tab', { name: 'AI', exact: true })).toHaveCount(0);

    await dialog.startAnyway();
    await dialog.waitForClosed();

    const aiTab = app.tabStrip.tab('AI');
    await app.tabStrip.waitForTab('AI');
    await aiTab.click();

    // The banner renders from the idle-status poll, so wait on the test budget.
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

  // A truncated activity snapshot must not fail the status read open and let a
  // second agent start under a held lease.
  test('a truncated activity snapshot does not hide a held lease', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    writeHeldLease(tenant, environment, OCCUPANT_LEASE);
    writeTruncatedActivitySnapshot(tenant, environment, 'mcp');

    await app.sidebar.openEnvironment(tenant, environment);

    const dialog = app.aiOccupancyPromptDialog;
    await dialog.waitForOpen();
    await expect(dialog.locator()).toContainText(OCCUPANT_LEASE);
    await expect(page.getByRole('tab', { name: 'AI', exact: true })).toHaveCount(0);

    await dialog.cancel();
    await dialog.waitForClosed();
    removeActivitySnapshot(tenant, environment, 'mcp');
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

  // "View jobs" is offered only when a real job record backs the lease, not
  // merely a job-shaped lease id.
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

    // The idle-status poll (1s) picks up the swapped lease.
    await expect(viewJobs).toBeVisible(withTestBudget());

    removeHeldLease(tenant, environment, JOB_LEASE);
    removeCompletedJob(tenant, environment, JOB_ID);
  });

  // The idle poll is held past expect's 10s default, so a banner assertion not
  // using the test budget would fail here.
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

    // Wait for one poll so the route covers only requests sent after it, which
    // cannot carry the lease staged below.
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

    // The hold is the stimulus, sized against expect's 10s default.
    setTimeout(releaseIdlePoll, 15_000);
    await expect(page.getByText('Another agent is working here')).toBeVisible(withTestBudget());

    removeHeldLease(tenant, environment, OCCUPANT_LEASE);
  });
});
