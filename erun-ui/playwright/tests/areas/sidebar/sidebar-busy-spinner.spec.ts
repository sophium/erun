import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ENV_BETA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Deploy spinners need a live cluster, so those tests lock negative invariants; build/release/push
// entries are trace-driven and staged directly.

test.describe('sidebar busy spinner', () => {
  test('quiet env rows show no spinner', async ({ app: _app, page }) => {
    // Avoid unrelated role="status" surfaces like terminal banners.
    const sidebar = page.locator('aside').first();
    const spinners = sidebar.getByRole('status');
    await expect(spinners).toHaveCount(0);
  });

  test('navigating away clears any in-flight spinner', async ({ app, page }) => {
    // Switching envs mid-open must not strand a spinner or banner on the previous env.
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_BETA);

    const sidebar = page.locator('aside').first();
    const spinners = sidebar.getByRole('status');
    await expect(spinners).toHaveCount(0);
  });

  test('running build/release/push entries surface a labelled spinner that clears on finish', async ({
    app: _app,
    page,
  }) => {
    const tenant = SEED_TENANT;
    const environment = SEED_ENV_ALPHA;

    const sidebar = page.locator('aside').first();
    // The harness may still be auto-opening the restored env, so assert on the per-command label
    // rather than zero spinners.

    const emitActivity = (command: string, status: 'running' | 'succeeded') =>
      page.evaluate(
        ({ command, status, tenant, environment }) => {
          const runtime = (
            window as unknown as {
              runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
            }
          ).runtime;
          const now = new Date().toISOString();
          runtime.EventsEmit('activity:state', {
            id: `spinner-spec-${command}`,
            command,
            tenant,
            environment,
            status,
            startedAt: now,
            lastUpdated: now,
            endedAt: status === 'running' ? undefined : now,
            source: 'trace',
            summary: `${command} ${tenant}/${environment}`,
          });
        },
        { command, status, tenant, environment },
      );

    for (const { command, verb } of [
      { command: 'build', verb: 'Building' },
      { command: 'release', verb: 'Releasing' },
      { command: 'push', verb: 'Pushing' },
    ]) {
      const labelledSpinner = sidebar.getByRole('status', {
        name: `${verb} ${tenant} / ${environment}`,
      });
      await emitActivity(command, 'running');
      await expect(labelledSpinner).toBeVisible();

      await emitActivity(command, 'succeeded');
      await expect(labelledSpinner).toHaveCount(0);
    }
  });
});
