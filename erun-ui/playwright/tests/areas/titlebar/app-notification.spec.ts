import { expect, test } from '../../../fixtures/erunApp.js';

// `app-notification` feeds the titlebar message centre: success/info
// auto-dismiss into history, warning/error persist as unread.

function emit(
  page: import('@playwright/test').Page,
  payload: Record<string, unknown>,
): Promise<void> {
  return page.evaluate((notification) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('app-notification', notification);
  }, payload);
}

test.describe('app-notification message centre', () => {
  test('info notification shows an unread icon, then auto-dismisses into history', async ({
    app,
    page,
  }) => {
    const message = 'Stopped idle cloud context cluster-cloud.';
    await emit(page, { kind: 'info', message });

    await expect(app.titlebar.messageCenterIcon('info')).toBeVisible();

    // Auto-dismiss marks it read; another spec's unread notice may hide the
    // history fallback, so assert only that some entry point remains.
    await expect(app.titlebar.messageCenterIcon('info')).toHaveCount(0);
    await expect(app.titlebar.messageCenterEntryPoint()).toBeVisible();
    await app.titlebar.openMessageCenterFromTitlebar();
    await expect(app.titlebar.messageCenterRow(message)).toBeVisible();
  });

  test('error notification persists and is readable in the dialog', async ({ app, page }) => {
    const message = 'Backend pinned a problem you should read.';
    await page.clock.install();
    await emit(page, { kind: 'error', message });

    const icon = app.titlebar.messageCenterIcon('error');
    await expect(icon).toBeVisible();
    await expect(icon).toHaveAccessibleName('Error: 1 unread');

    await page.clock.fastForward(5_000);
    await expect(icon).toBeVisible();

    await app.titlebar.openMessageCenter('error');
    await expect(app.titlebar.messageCenterRow(message)).toBeVisible();
  });

  test('warning notification persists and stays copyable from the dialog', async ({
    app,
    page,
  }) => {
    // The emit itself is owned by TestSpawnOrchestratorSignalsUnwiredEnvironments (Go).
    const message =
      'Petios started without its environment tools: no linked environment resolved an MCP port. Check its linked environments still exist, then restart the orchestrator.';
    await page.clock.install();
    await emit(page, { kind: 'warning', message });

    const icon = app.titlebar.messageCenterIcon('warning');
    await expect(icon).toBeVisible();
    await page.clock.fastForward(5_000);
    await expect(icon).toBeVisible();

    await app.titlebar.openMessageCenter('warning');
    const row = app.titlebar.messageCenterRow(message);
    await expect(row).toBeVisible();
    await expect(row.getByRole('button', { name: 'Copy', exact: true })).toBeVisible();
  });

  test('skills-not-installed warning persists and names the recovery, full text, no truncation', async ({
    app,
    page,
  }) => {
    // The emit itself is owned by TestOrchestratorSkillsReportUnresolvableSource (Go).
    const message =
      'Orchestrator skills were not installed or refreshed: no erun skills source resolved: ' +
      'its build checkout /Users/op/src/erun/erun-skills/skills is not on this machine, and no ' +
      'erun-skills/skills sits above /Users/op/.cache/erun/dev-bin/ERun.app/Contents/MacOS. ' +
      'The orchestrator still starts, but its skills stay at whatever is already in ~/.claude/skills. ' +
      'Set ERUN_SKILLS_DIR to an erun-skills/skills directory to install from, ' +
      'or rebuild the desktop from its checkout with erun-ui/build.sh (build.ps1 on Windows).';
    await page.clock.install();
    await emit(page, { kind: 'warning', message });

    const icon = app.titlebar.messageCenterIcon('warning');
    await expect(icon).toBeVisible();
    await page.clock.fastForward(5_000);
    await expect(icon).toBeVisible();

    await app.titlebar.openMessageCenter('warning');
    const row = app.titlebar.messageCenterRow('Orchestrator skills were not');
    await expect(row).toBeVisible();
    await expect(row).toContainText('Set ERUN_SKILLS_DIR');
    await expect(row.getByRole('button', { name: 'Copy', exact: true })).toBeVisible();
  });

  // Each unreachable edge posts its own notice with a Deploy action; the
  // actionless shape renders none. The emit is owned by
  // TestWireOrchestratorMCPMultipleUnreachableEnvsAreEachActionable (Go).
  test('each unreachable-edge warning carries its own Deploy control', async ({ app, page }) => {
    const recovery =
      'Calls will recover automatically once the edge comes back; if it stays down, deploy or reopen that environment.';
    for (const [tenant, environment] of [
      ['pw', 'alpha'],
      ['pw', 'beta'],
    ]) {
      await emit(page, {
        kind: 'warning',
        message: `Petios wired tools for ${tenant}/${environment}, but its edge is not answering right now. ${recovery}`,
        tenant,
        environment,
        source: 'orchestrator-edge-unreachable',
        action: 'deploy',
      });
    }
    await emit(page, {
      kind: 'warning',
      message: `Petios wired tools for pw/alpha, pw/beta, but its edge is not answering right now. ${recovery}`,
    });

    await app.titlebar.openMessageCenter('warning');

    for (const [tenant, environment] of [
      ['pw', 'alpha'],
      ['pw', 'beta'],
    ]) {
      const row = app.titlebar.messageCenterRow(
        `wired tools for ${tenant}/${environment}, but its edge`,
      );
      await expect(row).toBeVisible();
      await expect(row.getByRole('button', { name: 'Deploy', exact: true })).toBeVisible();
    }

    const actionless = app.titlebar.messageCenterRow('wired tools for pw/alpha, pw/beta');
    await expect(actionless).toBeVisible();
    await expect(actionless.getByRole('button', { name: 'Deploy', exact: true })).toHaveCount(0);
  });

  test('payload with empty message is ignored', async ({ app, page }) => {
    const sentinel = 'Sentinel error toast proving the empty payload was processed.';
    // Events are ordered, so the sentinel bounds the "nothing happened" assertion.
    await emit(page, { kind: 'info', message: '   ' });
    await emit(page, { kind: 'error', message: sentinel });

    await expect(app.titlebar.messageCenterIcon('error')).toBeVisible();
    await expect(app.titlebar.messageCenterIcon('info')).toHaveCount(0);
    await expect(app.titlebar.messageCenterHistoryButton()).toHaveCount(0);
  });

  // Concurrent notifications must not overwrite one another.
  test('five concurrent error notifications all remain readable, none overwritten', async ({
    app,
    page,
  }) => {
    const messages = Array.from({ length: 5 }, (_, i) => `Concurrent failure marker ${String(i)}`);
    for (const message of messages) {
      await emit(page, { kind: 'error', message });
    }

    const icon = app.titlebar.messageCenterIcon('error');
    await expect(icon).toHaveAccessibleName('Error: 5 unread');
    await app.titlebar.openMessageCenter('error');

    for (const message of messages) {
      await expect(app.titlebar.messageCenterRow(message)).toBeVisible();
    }
  });
});
