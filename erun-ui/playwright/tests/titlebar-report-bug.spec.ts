import type { Request, Route } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// erun#1591: the toolbar's error pill had a named remedy for a handful of
// causes ('wait-longer', 'deploy', restart-orchestrator) and nothing at all
// for every other error — the operator read the message and was on their
// own. This locks the fix: "Report a bug" is now a standing action on every
// error status, ordered after a known remedy (never ahead of it), and
// clicking it hands the failure to an agent rather than opening a form.
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
  test('an error with no known remedy still offers a reporting action', async ({
    app,
    page,
  }) => {
    await emitErrorNotification(page, { message: 'Could not reach the runtime.' });
    await expect(app.titlebar.errorAlert()).toBeVisible();
    await expect(app.titlebar.reportBugButton()).toBeVisible();
    // No remedy was named, so nothing else in the pill's action row precedes it.
    await expect(app.titlebar.deployActionButton()).toHaveCount(0);
  });

  test('a warning status is not treated as an error and gets no reporting action', async ({
    app,
    page,
  }) => {
    await page.evaluate(() => {
      const runtime = (
        window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
      ).runtime;
      runtime.EventsEmit('app-notification', { kind: 'warning', message: 'Idle soon.' });
    });
    await expect(app.titlebar.statusMessage()).toBeVisible();
    await expect(app.titlebar.reportBugButton()).toHaveCount(0);
  });

  test('a known remedy leads, and the reporting action follows it', async ({ app, page }) => {
    await emitErrorNotification(page, {
      message: 'The runtime is unreachable.',
      action: 'deploy',
      tenant: SEED_TENANT,
      environment: SEED_ENV_ALPHA,
    });
    const deploy = app.titlebar.deployActionButton();
    const reportBug = app.titlebar.reportBugButton();
    await expect(deploy).toBeVisible();
    await expect(reportBug).toBeVisible();

    const labels = await app.titlebar
      .errorAlert()
      .evaluate((alert) =>
        Array.from(alert.querySelectorAll('button')).map((button) => button.textContent?.trim() ?? ''),
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
    await emitErrorNotification(page, { message: 'Could not reach the runtime.' });
    await expect(app.titlebar.reportBugButton()).toBeVisible();

    const [popup] = await Promise.all([
      page.waitForEvent('popup'),
      app.titlebar.reportBugButton().click(),
    ]);
    await expect.poll(() => popup.url()).toContain('github.com/sophium/erun/issues/new');
    await popup.close();

    // The failed draft's own notification is replaced by the fallback's, which
    // names both the refusal and that the browser path opened instead — never
    // a silent downgrade.
    await expect(app.titlebar.statusMessage()).toContainText('no diagnostic content');
    await expect(app.titlebar.statusMessage()).toContainText('Opened a prefilled issue');
  });

  test('a refusal naming an already-running draft is surfaced rather than treated as an error', async ({
    app,
    page,
  }) => {
    await mockReportBugFailure(page, {
      admitted: false,
      reason: 'already-investigating',
      message: 'That failure is already under investigation as report-bug-1, started 2s ago.',
      existingId: 'report-bug-1',
    });
    await emitErrorNotification(page, { message: 'Could not reach the runtime.' });
    await app.titlebar.reportBugButton().click();
    await expect(app.titlebar.statusMessage()).toContainText('already under investigation');
  });
});
