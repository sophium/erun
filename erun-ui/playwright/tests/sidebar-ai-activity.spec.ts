import { expect, test } from '../fixtures/erunApp.js';

// sidebar-ai-activity covers the new "AI tab is working" signal that
// drives the sidebar env-row busy badge while a Claude/Codex
// generation is in flight on a tab the user has navigated away from.
//
// The signal is the debounced `ai-activity` Wails event emitted by
// recordAIActivity in erun-ui/terminal_sessions.go (busy=true after
// >=5 s of sustained AI-PTY output, busy=false after >=3 s of silence
// or on session close). The frontend slice aiActivitySlice maps
// payloads to `aiBusyByEnv[selectionKey(...)]`; Sidebar.helpers.ts
// factors that into the deriveEnvironmentRow busy bit so an env row
// surfaces a spinner without requiring the user to be looking at that
// env's tabs.
//
// The Go-side debounce timing is not reachable from the headless
// harness (it requires a live PTY producing output for 5+ seconds).
// This spec drives the same code path the production event ends up
// using — emit the Wails event directly and verify the sidebar
// reflects it — and covers both the on and off transitions. The Go
// debounce logic itself is covered by the erun-ui app_test.go suite.

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

    // Baseline: no spinner on quiet rows.
    const sidebar = page.locator('aside').first();
    await expect(sidebar.getByRole('status')).toHaveCount(0);

    // Emit busy=true via the Wails ai-activity event. The handler in
    // wailsEventThunks.ts builds the selectionKey from {tenant, env}
    // and the env-row selector matches the same key.
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
    await expect(spinner).toBeVisible({ timeout: 5_000 });
    await expect(spinner).toHaveAttribute('aria-label', new RegExp(`${env}`));

    // Emit busy=false; the spinner must disappear.
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
    await expect(sidebar.getByRole('status')).toHaveCount(0, { timeout: 5_000 });
  });

  test('ai-activity payloads with empty tenant or environment are ignored', async ({
    app,
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
    // No env row matches an empty selectionKey, so no spinner appears.
    await expect(sidebar.getByRole('status')).toHaveCount(0);
  });
});
