import { expect, test } from '../../../fixtures/erunApp.js';

// The Go debounce needs a live PTY, so this emits the ai-activity event directly; debounce timing
// is covered by app_test.go.

test.describe('sidebar AI activity spinner', () => {
  test('ai-activity busy=true surfaces a spinner on the env row, busy=false clears it', async ({
    app,
    page,
  }) => {
    const tenants = await app.sidebar.tenants();
    expect(tenants.length).toBeGreaterThan(0);
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    expect(envs.length).toBeGreaterThan(0);
    const env = envs[0]!;

    const sidebar = page.locator('aside').first();
    await expect(sidebar.getByRole('status')).toHaveCount(0);

    await page.evaluate(
      ({ tenant, env }) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('ai-activity', {
          sessionId: 99,
          tenant,
          environment: env,
          busy: true,
        });
      },
      { tenant, env },
    );

    const spinner = sidebar.getByRole('status').first();
    await expect(spinner).toBeVisible();
    await expect(spinner).toHaveAttribute('aria-label', new RegExp(`${env}`));

    await page.evaluate(
      ({ tenant, env }) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('ai-activity', {
          sessionId: 99,
          tenant,
          environment: env,
          busy: false,
        });
      },
      { tenant, env },
    );
    await expect(sidebar.getByRole('status')).toHaveCount(0);
  });

  test('ai-activity payloads with empty tenant or environment are ignored', async ({
    app: _app,
    page,
  }) => {
    const sidebar = page.locator('aside').first();
    await page.evaluate(() => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('ai-activity', {
        sessionId: 99,
        tenant: '',
        environment: '',
        busy: true,
      });
    });
    await expect(sidebar.getByRole('status')).toHaveCount(0);
  });

  test('closing an env clears a latched AI-activity spinner', async ({ app, page, seededEnv }) => {
    const { tenant, environment } = seededEnv;
    const sidebar = page.locator('aside').first();
    const envSpinner = sidebar.getByRole('status', {
      name: new RegExp(`${tenant} / ${environment}`),
    });

    await app.sidebar.openEnvironment(tenant, environment);
    const closeDot = sidebar.getByRole('button', { name: `Close ${tenant} / ${environment}` });
    await expect(closeDot).toBeVisible();
    await expect(envSpinner).toHaveCount(0);

    // A real Claude TUI repaints continuously, so the latch never idle-clears; emulate that stuck
    // busy=true.
    await page.evaluate(
      ({ tenant, environment }) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('ai-activity', { sessionId: 99, tenant, environment, busy: true });
      },
      { tenant, environment },
    );
    await expect(envSpinner).toBeVisible();

    // Closing the env must clear the busy latch.
    await closeDot.click();
    await expect(envSpinner).toHaveCount(0);
  });
});
