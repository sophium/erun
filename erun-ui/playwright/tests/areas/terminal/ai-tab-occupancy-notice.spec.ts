import { test, expect } from '../../../fixtures/erunApp.js';
import { removeHeldLease, writeHeldLease } from '../../../fixtures/seedRoot.js';

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
});
