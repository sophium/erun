import type { Request, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// erun#1591: the toolbar's error pill had a named remedy for a handful of
// causes ('wait-longer', 'deploy', restart-orchestrator) and nothing at all
// for every other error — the operator read the message and was on their
// own. This locks the fix: "Report a bug" is now a standing action on every
// error message centre row, ordered after a known remedy (never ahead of
// it), and clicking it hands the failure to an agent rather than opening a
// form. The message centre moved every notification's actions from the old
// inline pill into the dialog a class icon opens; the assertions
// below target that dialog now, not the pill.
//
// The agent-drafting path itself (ReportBugFailure admitting and spawning a
// real orchestrator) is not driven here for the same reason
// investigate-spawn-bounds.spec.ts stays on the refusing side only: the
// harness stubs kubectl/helm/docker/aws but not the AI tool, so admitting a
// real session here would leave an agent behind on every run. That side is
// covered against a stubbed terminal in erun-ui/report_bug_test.go. This spec
// mocks ReportBugFailure's *response* to drive the desktop's reaction to each
// outcome shape without ever touching a real backend admission.

function emitErrorNotification(
  page: import('@playwright/test').Page,
  payload: { message: string; action?: string; tenant?: string; environment?: string },
): Promise<void> {
  return page.evaluate((notification) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('app-notification', { kind: 'error', ...notification });
  }, payload);
}

async function mockReportBugFailure(
  page: import('@playwright/test').Page,
  outcome: Record<string, unknown>,
): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method !== 'ReportBugFailure') {
      await route.continue();
      return;
    }
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ data: outcome }),
    });
  });
}

test.describe('titlebar "Report a bug" action', () => {
  // This is the case that was broken before #1591: an error with no known
  // remedy rendered no action at all, so the operator had nothing to do about
  // it. A test that only checked an error *with* a remedy would still pass
  // while that dead end shipped.
  test('an error with no known remedy still offers a reporting action', async ({ app, page }) => {
    const message = 'Could not reach the runtime.';
    await emitErrorNotification(page, { message });
    await app.titlebar.openMessageCenter('error');
    const row = app.titlebar.messageCenterRow(message);
    await expect(row).toBeVisible();
    await expect(row.getByRole('button', { name: /^Report a bug/ })).toBeVisible();
    // No remedy was named, so nothing else in the row's action group precedes it.
    await expect(row.getByRole('button', { name: 'Deploy', exact: true })).toHaveCount(0);
  });

  test('a warning message is not treated as an error and gets no reporting action', async ({
    app,
    page,
  }) => {
    const message = 'Idle soon.';
    await page.evaluate((msg) => {
      const runtime = (
        window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
      ).runtime;
      runtime.EventsEmit('app-notification', { kind: 'warning', message: msg });
    }, message);
    await app.titlebar.openMessageCenter('warning');
    const row = app.titlebar.messageCenterRow(message);
    await expect(row).toBeVisible();
    await expect(row.getByRole('button', { name: /^Report a bug/ })).toHaveCount(0);
  });

  test('a known remedy leads, and the reporting action follows it', async ({ app, page }) => {
    const message = 'The runtime is unreachable.';
    await emitErrorNotification(page, {
      message,
      action: 'deploy',
      tenant: SEED_TENANT,
      environment: SEED_ENV_ALPHA,
    });
    await app.titlebar.openMessageCenter('error');
    const row = app.titlebar.messageCenterRow(message);
    const deploy = row.getByRole('button', { name: 'Deploy', exact: true });
    const reportBug = row.getByRole('button', { name: /^Report a bug/ });
    await expect(deploy).toBeVisible();
    await expect(reportBug).toBeVisible();

    const labels = await row.evaluate((el) =>
      Array.from(el.querySelectorAll('button')).map((button) => button.textContent?.trim() ?? ''),
    );
    const deployIndex = labels.findIndex((text) => text.includes('Deploy'));
    const reportIndex = labels.findIndex((text) => text.includes('Report a bug'));
    expect(deployIndex).toBeGreaterThanOrEqual(0);
    expect(reportIndex).toBeGreaterThan(deployIndex);
  });

  test('a refusal with no existing draft falls back to the prefilled issue URL, naming why', async ({
    app,
    page,
  }) => {
    await mockReportBugFailure(page, {
      admitted: false,
      reason: 'thin-report',
      message: 'This failure report carries no diagnostic content.',
    });
    const message = 'Could not reach the runtime.';
    await emitErrorNotification(page, { message });
    await app.titlebar.openMessageCenter('error');
    const row = app.titlebar.messageCenterRow(message);
    await expect(row.getByRole('button', { name: /^Report a bug/ })).toBeVisible();

    // This sandbox can reach the real github.com, which would otherwise
    // redirect the popup to a sign-in page before the URL is read (and
    // aborting the request instead navigates to chrome-error://, which is no
    // more stable) — fulfill it locally so the popup's URL never changes.
    await page
      .context()
      .route('https://github.com/**', (route) =>
        route.fulfill({ status: 200, contentType: 'text/plain', body: 'stubbed for the test' }),
      );
    const [popup] = await Promise.all([
      page.waitForEvent('popup'),
      row.getByRole('button', { name: /^Report a bug/ }).click(),
    ]);
    await expect.poll(() => popup.url()).toContain('github.com/sophium/erun/issues/new');
    await popup.close();

    // openFallbackReportURL (orchestratorThunks.ts) posts its own warning
    // notification naming both the refusal and that the browser path opened
    // instead — never a silent downgrade. It carries the same tenant/env
    // tag as the original error, so it lands in the same warning icon. The
    // dialog is modal, so the warning icon behind it is unreachable until the
    // error dialog closes -- exactly what a real operator would have to do too.
    await app.titlebar.closeMessageCenter();
    await app.titlebar.openMessageCenter('warning');
    const fallbackRow = app.titlebar.messageCenterRow('no diagnostic content');
    await expect(fallbackRow).toBeVisible();
    await expect(fallbackRow).toContainText('Opened a prefilled issue');
  });

  test('a refusal naming an already-running draft is surfaced rather than treated as an error', async ({
    app,
    page,
  }) => {
    // The refusal lands as an info notification (see below), which
    // auto-dismisses after TRANSIENT_DISMISS_MS -- freeze the clock so the
    // assertion can never race that timer in a slow CI run.
    await page.clock.install();
    await mockReportBugFailure(page, {
      admitted: false,
      reason: 'already-investigating',
      message: 'That failure is already under investigation as report-bug-1, started 2s ago.',
      existingId: 'report-bug-1',
    });
    const message = 'Could not reach the runtime.';
    await emitErrorNotification(page, { message });
    await app.titlebar.openMessageCenter('error');
    await app.titlebar
      .messageCenterRow(message)
      .getByRole('button', { name: /^Report a bug/ })
      .click();

    // orchestratorThunks.reportFailure posts the "already-investigating"
    // refusal as an info notification, not an error -- it names a draft that
    // already exists, not a fresh failure. The dialog is modal, so the info
    // icon behind it is unreachable until the error dialog closes.
    await app.titlebar.closeMessageCenter();
    await app.titlebar.openMessageCenter('info');
    await expect(app.titlebar.messageCenterRow('already under investigation')).toBeVisible();
  });
});
