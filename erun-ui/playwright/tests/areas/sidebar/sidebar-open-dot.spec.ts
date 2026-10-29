import { expect, test } from '../../../fixtures/erunApp.js';

// The open dot means "tabs open" and closes the env on click; it is independent of the LOCAL pill
// and busy spinner.

test.describe('sidebar env open dot', () => {
  test('a never-opened env row shows no open dot', async ({ app, seededEnv }) => {
    // Baseline rows may boot with reattached sessions; a seeded env is never opened.
    const row = app.sidebar.envRowButton(seededEnv.tenant, seededEnv.environment).locator('..');
    await expect(row.getByTestId('env-open-dot')).toHaveCount(0);
  });

  test('opening an env mounts the dot; clicking the dot closes the env', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);

    const sidebar = page.locator('aside').first();
    const dot = sidebar.getByRole('button', { name: `Close ${tenant} / ${environment}` });
    await expect(dot).toBeVisible();

    // Closing must not also trigger the row's openSelection; the POM re-activates through the
    // tooltip until closed.
    await app.sidebar.closeEnvironment(tenant, environment);
  });

  // The dot reflects real state by shape + label: running, stopped (cloud or runtime), and failed
  // are distinct.
  // These states can't be staged headless, so the spec drives the env-status event.
  test('the dot reflects the real env state: running → stopped → runtime-stopped → failed → running', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;

    // Wait for the first idle poll so the backend's settle-time env-status emissions are done
    // before simulating.
    const idlePolled = page.waitForResponse(
      (response) =>
        response.url().includes('/__erun_invoke') &&
        (response.request().postData() ?? '').includes('LoadIdleStatus'),
    );
    await app.sidebar.openEnvironment(tenant, environment);
    await idlePolled;
    const dot = app.sidebar.envOpenDot(tenant, environment);
    await expect(dot).toBeVisible();
    await expect(dot).toHaveAttribute('data-env-state', 'running');

    // The backend can emit its own env-status during settle; re-drive until both attribute and name
    // converge.
    const driveEnvStatus = async (
      status: string,
      expectedState: string,
      expectedName: string | RegExp,
    ): Promise<void> => {
      await expect(async () => {
        await emitEnvStatus(page, tenant, environment, status);
        await expect(dot).toHaveAttribute('data-env-state', expectedState, { timeout: 1_000 });
        await expect(dot).toHaveAccessibleName(expectedName, { timeout: 1_000 });
      }).toPass();
    };

    await driveEnvStatus(
      'stopped',
      'stopped',
      new RegExp(`^${tenant} / ${environment} is stopped`),
    );
    // A runtime scaled to zero is stopped, not failed, and names opening the env as the recovery.
    await driveEnvStatus(
      'runtime-stopped',
      'runtime-stopped',
      /is stopped — click it in the sidebar/,
    );
    await driveEnvStatus('failed', 'failed', /deploy failed/);
    await driveEnvStatus('', 'running', `Close ${tenant} / ${environment}`);

    // Return the singleton backend to its pre-test shape.
    await app.sidebar.closeEnvironment(tenant, environment);
  });

  // A failed deploy is the runtime's state, not the session's, so closing tabs must not hide it.
  test('a failed deploy stays visible after the env is closed', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);
    // Wait for the last default tab (AI) to spawn, or an in-flight spawn can remount the dot after
    // close.
    await page.getByRole('tab', { name: 'AI', exact: true }).waitFor({ state: 'visible' });
    await app.sidebar.closeEnvironment(tenant, environment);

    const row = app.sidebar.envRowButton(tenant, environment).locator('..');
    const dot = row.getByTestId('env-open-dot');
    await expect(dot).toHaveCount(0);

    await expect(async () => {
      await emitEnvStatus(page, tenant, environment, 'failed');
      await expect(dot).toHaveAttribute('data-env-state', 'failed', { timeout: 1_000 });
      await expect(dot).toHaveAttribute('data-env-opened', 'false', { timeout: 1_000 });
      await expect(dot).toHaveAccessibleName(/deploy failed/, { timeout: 1_000 });
    }).toPass();

    // A passive light: there is no tab left to close.
    await expect(dot).not.toHaveAccessibleName(new RegExp(`^Close ${tenant} / ${environment}`));

    // Clear the sticky flag for later specs.
    await emitEnvStatus(page, tenant, environment, '');
  });
});

// Mirrors the env-status event the Go side emits from tryReconnect's refusal paths and the
// open/respawn clears.
async function emitEnvStatus(
  page: import('@playwright/test').Page,
  tenant: string,
  environment: string,
  status: string,
): Promise<void> {
  await page.evaluate(
    (payload) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('env-status', payload);
    },
    { tenant, environment, status },
  );
}
