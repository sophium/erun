import { test, expect } from '../fixtures/erunApp.js';
import {
  removeCompletedJob,
  removeHeldLease,
  writeCompletedJob,
  writeHeldLease,
} from '../fixtures/seedRoot.js';

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

    const aiTab = page.getByRole('tab', { name: 'AI', exact: true });
    await aiTab.waitFor({ state: 'visible', timeout: 20_000 });
    await aiTab.click();

    // Persistent indicator (Nielsen #1: visibility of system status) while the
    // coexisting job is still held — not a one-time toast.
    await expect(page.getByText('Another agent is working here')).toBeVisible();

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
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);

    const aiTab = page.getByRole('tab', { name: 'AI', exact: true });
    await aiTab.waitFor({ state: 'visible', timeout: 20_000 });
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
    await aiTab.waitFor({ state: 'visible', timeout: 20_000 });
    await aiTab.click();

    await expect(page.getByText('Another agent is working here')).toBeVisible();
    const viewJobs = page.getByRole('button', {
      name: `Show the jobs running in ${environment}`,
    });
    await expect(viewJobs).toHaveCount(0);

    removeHeldLease(tenant, environment, HAND_LEASE);
    const JOB_ID = 'gate-9';
    const JOB_LEASE = `job-${JOB_ID}`;
    writeCompletedJob(tenant, environment, JOB_ID, 'repo gate');
    writeHeldLease(tenant, environment, JOB_LEASE);

    // The next idle-status poll (every 1s) picks up the swapped lease.
    await expect(viewJobs).toBeVisible({ timeout: 15_000 });

    removeHeldLease(tenant, environment, JOB_LEASE);
    removeCompletedJob(tenant, environment, JOB_ID);
  });
});
