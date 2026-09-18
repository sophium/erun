import { expect, test } from '../../../fixtures/erunApp.js';

// Covers the "AI tab is working" signal that surfaces a busy spinner on a
// sidebar env row while a Claude/Codex generation runs on a tab the user has
// navigated away from.
//
// The Go-side debounce that emits the real ai-activity event needs a live PTY
// producing output for 5+ seconds, which the headless harness cannot drive, so
// this spec emits the Wails event directly and asserts both the on and off
// transitions. The debounce timing itself is covered by erun-ui app_test.go.

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

    // Open the env so it has live tabs and the close dot mounts.
    await app.sidebar.openEnvironment(tenant, environment);
    const closeDot = sidebar.getByRole('button', { name: `Close ${tenant} / ${environment}` });
    await expect(closeDot).toBeVisible();
    await expect(envSpinner).toHaveCount(0);

    // A real Claude TUI repaints continuously, so the Go idle-clear never fires
    // and the ai-activity latch stays on; emulate that stuck busy=true.
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

    // Closing the env must clear the busy latch. Before the fix the spinner
    // stayed forever (green dot gone, row still spinning) because close never
    // finalized the AI session, so busy=false was never emitted.
    await closeDot.click();
    await expect(envSpinner).toHaveCount(0);
  });
});
