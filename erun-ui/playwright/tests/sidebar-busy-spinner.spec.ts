import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_ENV_BETA, SEED_TENANT } from '../fixtures/seedRoot.js';

// A deploy-triggered spinner cannot be staged in the headless harness (it
// needs the helm poller + live cluster state), so the first two tests only
// lock negative invariants. build / release / push entries are pure
// trace-driven with no cluster dependency, so the third test stages a
// running entry directly and drives the positive flow.

test.describe('sidebar busy spinner', () => {
  test('quiet env rows show no spinner', async ({ app: _app, page }) => {
    // Scope to the sidebar so the assertion does not collide with unrelated
    // role="status" surfaces like terminal banners.
    const sidebar = page.locator('aside').first();
    const spinners = sidebar.getByRole('status');
    await expect(spinners).toHaveCount(0);
  });

  test('navigating away clears any in-flight spinner', async ({ app, page }) => {
    // Guards a regression where clicking env A then env B mid-open (a cold
    // EC2 open can take ~60s) stranded a stale spinner and "Opening A..."
    // banner on the env the user had navigated away from.
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
    // No "zero spinners" precondition: the harness may still be auto-opening
    // the restored env (its own "Opening..." spinner), unrelated to this
    // test. Asserting on the specific per-command label keeps the checks
    // robust to that concurrent open.

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

      // Scope to the command label so a concurrent "Opening..." spinner on
      // the same row cannot make the finish assertion flaky.
      await emitActivity(command, 'succeeded');
      await expect(labelledSpinner).toHaveCount(0);
    }
  });
});
