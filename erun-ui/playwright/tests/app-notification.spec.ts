import { expect, test } from '../fixtures/erunApp.js';

// The `app-notification` event surfaces one-shot info/success events as
// transient, auto-dismissing toasts. It exists because the idle auto-stop
// success previously rode the persistent `app-status` channel, which latched
// the message into the titlebar pill long after its cloud context had been
// restarted elsewhere.

test.describe('app-notification toast', () => {
  test('info notification renders in the titlebar then auto-dismisses', async ({
    app: _app,
    page,
  }) => {
    const message = 'Stopped idle cloud context cluster-cloud.';

    await page.evaluate(
      (payload) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('app-notification', payload);
      },
      { kind: 'info', message },
    );

    const pill = page.getByRole('status').filter({ hasText: message });
    await expect(pill).toBeVisible();

    await expect(pill).toHaveCount(0);
  });

  test('error notification persists (no auto-dismiss)', async ({ app: _app, page }) => {
    const message = 'Backend pinned a problem you should read.';
    // The notification slot is single-occupancy, so the error toast can't be
    // timed against a sibling info toast — advance the clock past the
    // auto-dismiss window instead of racing a second toast.
    await page.clock.install();
    await page.evaluate(
      (payload) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('app-notification', payload);
      },
      { kind: 'error', message },
    );

    const pill = page.getByRole('alert').filter({ hasText: message });
    await expect(pill).toBeVisible();

    await page.clock.fastForward(5_000);
    await expect(pill).toBeVisible();
  });

  test('warning notification persists and stays copyable', async ({ app: _app, page }) => {
    // An orchestrator that launched without the tools for its linked
    // environments posts a warning here, and the operator must still be able to
    // read the cause after the session is up — a warning that auto-dismissed
    // would put the diagnosis back where it was, in the log. The emit itself is
    // owned by TestSpawnOrchestratorSignalsUnwiredEnvironments (Go): spawning an
    // orchestrator needs a real AI harness and PTY, which this harness lacks.
    const message =
      'Petios started without its environment tools: no linked environment resolved an MCP port. Check its linked environments still exist, then restart the orchestrator.';
    await page.clock.install();
    await page.evaluate(
      (payload) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('app-notification', payload);
      },
      { kind: 'warning', message },
    );

    const pill = page.getByRole('status').filter({ hasText: message });
    await expect(pill).toBeVisible();

    await page.clock.fastForward(5_000);
    await expect(pill).toBeVisible();
    await expect(pill.getByRole('button', { name: /copy/i })).toBeVisible();
  });

  test('skills-not-installed warning persists and names the recovery', async ({
    app: _app,
    page,
  }) => {
    // A desktop that cannot resolve the skills it ships stops refreshing the
    // installed copies, and the launch itself looks untroubled — so the warning
    // has to stay readable until the operator acts on it, recovery included.
    // The emit is owned by TestOrchestratorSkillsReportUnresolvableSource (Go):
    // it fires while an orchestrator's workspace is prepared, which needs a real
    // AI harness and PTY that this harness deliberately lacks.
    const message =
      'Orchestrator skills were not installed or refreshed: no erun skills source resolved: ' +
      'its build checkout /Users/op/src/erun/erun-skills/skills is not on this machine, and no ' +
      'erun-skills/skills sits above /Users/op/.cache/erun/dev-bin/ERun.app/Contents/MacOS. ' +
      'The orchestrator still starts, but its skills stay at whatever is already in ~/.claude/skills. ' +
      'Set ERUN_SKILLS_DIR to an erun-skills/skills directory to install from, ' +
      'or rebuild the desktop from its checkout with erun-ui/build.sh (build.ps1 on Windows).';
    await page.clock.install();
    await page.evaluate(
      (payload) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('app-notification', payload);
      },
      { kind: 'warning', message },
    );

    const pill = page.getByRole('status').filter({ hasText: 'Orchestrator skills were not' });
    await expect(pill).toBeVisible();

    await page.clock.fastForward(5_000);
    await expect(pill).toBeVisible();
    await expect(pill).toContainText('Set ERUN_SKILLS_DIR');
    await expect(pill.getByRole('button', { name: /copy/i })).toBeVisible();
  });

  test('payload with empty message is ignored', async ({ app: _app, page }) => {
    // The titlebar idle-status widget also carries role=status when an env with
    // a managed cloud context is active, so an ignored payload can't be checked
    // against an absolute count of zero — compare before/after instead.
    const statusBefore = await page.locator('[role="status"]').count();
    const alertBefore = await page.locator('[role="alert"]').count();
    const sentinel = 'Sentinel error toast proving the empty payload was processed.';
    // Events are ordered, so once this error sentinel renders the empty dispatch
    // has provably been processed — a real event bounds the "nothing happened"
    // assertion instead of a sleep. Error kind so the sentinel never
    // auto-dismisses mid-assertion.
    await page.evaluate((msg) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('app-notification', { kind: 'info', message: '   ' });
      runtime.EventsEmit('app-notification', { kind: 'error', message: msg });
    }, sentinel);
    await expect(page.getByRole('alert').filter({ hasText: sentinel })).toBeVisible();
    await expect(page.locator('[role="status"]')).toHaveCount(statusBefore);
    await expect(page.locator('[role="alert"]')).toHaveCount(alertBefore + 1);
  });

  // The notification slot used to be a single `AppNotification | null` value,
  // so a burst of concurrent failures (e.g. several "Upgrade all" members
  // failing within milliseconds of each other) silently overwrote one
  // another — only the last dispatch survived. Five concurrent error toasts
  // must all remain readable: the titlebar shows one at a time, and
  // dismissing it reveals a still-queued one rather than it having been lost.
  // Delivery order across the fake-backend event bridge isn't guaranteed (it
  // interleaves these five arbitrarily), so this asserts the set of five is
  // fully seen — not that they arrive in a specific order.
  test('five concurrent error notifications all remain readable, none overwritten', async ({
    app: _app,
    page,
  }) => {
    const messages = Array.from({ length: 5 }, (_, i) => `Concurrent failure marker ${String(i)}`);
    await page.evaluate((msgs) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
        }
      ).runtime;
      for (const message of msgs) {
        runtime.EventsEmit('app-notification', { kind: 'error', message });
      }
    }, messages);

    const seen = new Set<string>();
    for (let i = 0; i < messages.length; i++) {
      const pill = page.getByRole('alert').filter({ hasText: /Concurrent failure marker \d/ });
      await expect(pill).toBeVisible();
      const text = (await pill.textContent()) ?? '';
      seen.add((/Concurrent failure marker \d/.exec(text) ?? [''])[0]);
      await pill.getByRole('button', { name: 'Dismiss status' }).click();
    }
    // All five distinct failures were shown at some point across the dismiss
    // sequence — none silently dropped by an overwrite.
    expect([...seen].sort()).toEqual(messages.slice().sort());
    await expect(
      page.getByRole('alert').filter({ hasText: /Concurrent failure marker/ }),
    ).toHaveCount(0);
  });
});
