import { expect, test } from '../../../fixtures/erunApp.js';

// The `app-notification` event feeds the titlebar's message centre: every
// notification gets a classified icon-with-count, and its
// full text/actions live in the review dialog the icon opens. This used to
// render as a single inline pill; it exists because the idle auto-stop
// success previously rode the persistent `app-status` channel, which latched
// the message into that pill long after its cloud context had been
// restarted elsewhere -- the transient/persistent split (success/info
// auto-dismiss, warning/error persist) still holds, it just now drives an
// icon's unread count and a dialog row instead of pill visibility.

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

    // Auto-dismiss marks it read (unread count -> 0), so the icon disappears
    // -- but the entry survives in history, so the titlebar still offers a way
    // into the message centre instead of going fully quiet. WHICH way is not
    // this spec's to pin: the "Message history" fallback renders only while no
    // class at all is unread, so a notice another spec left arriving in this
    // worker's page keeps that class's icon up and the fallback off. Assert
    // the reachability the spec actually owns, then use the entry point to
    // read the entry back out of history.
    // titlebar-message-centre-entry-point.spec.ts drives both titlebar shapes
    // deterministically in one page.
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
    // An orchestrator that launched without the tools for its linked
    // environments posts a warning here, and the operator must still be able
    // to read the cause after the session is up. The emit itself is owned by
    // TestSpawnOrchestratorSignalsUnwiredEnvironments (Go): spawning an
    // orchestrator needs a real AI harness and PTY, which this harness lacks.
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
    // A desktop that cannot resolve the skills it ships stops refreshing the
    // installed copies, and the launch itself looks untroubled -- so the
    // warning has to stay readable, recovery included. The emit is owned by
    // TestOrchestratorSkillsReportUnresolvableSource (Go): it fires while an
    // orchestrator's workspace is prepared, which needs a real AI harness and
    // PTY this harness deliberately lacks.
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
    // The dialog carries the full sentence untruncated -- the message centre
    // retires the pill's LONG_STATUS_THRESHOLD escalation for
    // notification-channel messages entirely, so there is no separate
    // "click to expand" step.
    const row = app.titlebar.messageCenterRow('Orchestrator skills were not');
    await expect(row).toBeVisible();
    await expect(row).toContainText('Set ERUN_SKILLS_DIR');
    await expect(row.getByRole('button', { name: 'Copy', exact: true })).toBeVisible();
  });

  // A launch that finds MORE than one linked environment's edge unreachable
  // used to collapse into one combined notice emitted with no action, so the
  // orchestrators with the most edges down were told about them with nothing
  // to click. The backend now posts one env-scoped notice per unreachable edge
  // (Go: TestWireOrchestratorMCPMultipleUnreachableEnvsAreEachActionable owns
  // that emit -- launching an orchestrator needs a real AI harness and PTY
  // this harness deliberately lacks). What is observable here is the half that
  // made the missing action a defect: each notice renders its own Deploy
  // control, and the actionless shape renders none -- the frontend's remedy
  // comes from the payload's action, not from the message naming a fix.
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
    // Events are ordered, so once this error sentinel's icon renders the
    // empty dispatch has provably been processed -- a real event bounds the
    // "nothing happened" assertion instead of a sleep.
    await emit(page, { kind: 'info', message: '   ' });
    await emit(page, { kind: 'error', message: sentinel });

    await expect(app.titlebar.messageCenterIcon('error')).toBeVisible();
    await expect(app.titlebar.messageCenterIcon('info')).toHaveCount(0);
    await expect(app.titlebar.messageCenterHistoryButton()).toHaveCount(0);
  });

  // The notification slot used to be a single `AppNotification | null` value,
  // so a burst of concurrent failures (e.g. several "Upgrade all" members
  // failing within milliseconds of each other) silently overwrote one
  // another -- only the last dispatch survived. Five concurrent errors must
  // all remain readable: the icon's count reflects every one of them, and
  // the dialog lists each distinctly rather than any being lost.
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
