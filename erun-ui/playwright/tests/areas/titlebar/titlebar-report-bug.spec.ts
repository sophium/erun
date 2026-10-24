import type { Request, Route } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// "Report a bug" is on every error row, after any known remedy. Admission is
// covered in report_bug_test.go (a real admission would leave an agent
// running), so ReportBugFailure's response is mocked here.

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
  // An error with no known remedy must still offer an action.
  test('an error with no known remedy still offers a reporting action', async ({ app, page }) => {
    const message = 'Could not reach the runtime.';
    await emitErrorNotification(page, { message });
    await app.titlebar.openMessageCenter('error');
    const row = app.titlebar.messageCenterRow(message);
    await expect(row).toBeVisible();
    await expect(row.getByRole('button', { name: /^Report a bug/ })).toBeVisible();
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

    // Fulfill github.com locally so a sign-in redirect cannot change the popup URL.
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

    // The fallback posts a warning naming the refusal; close the modal to reach it.
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
    // The refusal is an auto-dismissing info notification; freeze the clock.
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

    // An existing draft is reported as info, not an error.
    await app.titlebar.closeMessageCenter();
    await app.titlebar.openMessageCenter('info');
    await expect(app.titlebar.messageCenterRow('already under investigation')).toBeVisible();
  });
});
