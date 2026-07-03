import { expect, test } from '../fixtures/erunApp.js';

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
});
