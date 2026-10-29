import type { Request, Route } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// An all-pushed report auto-dismisses; anything else stays open. Duplicated
// from frontend's transientDismissDuration.ts; keep in lockstep.
const TRANSIENT_DISMISS_MS = 3200;

async function mockWhipReport(
  page: import('@playwright/test').Page,
  results: Array<{ kind: string; id: string; name: string; outcome: string }>,
): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method !== 'WhipNow') {
      await route.continue();
      return;
    }
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ data: { results } }),
    });
  });
}

// The dismiss timer arms only while document.hasFocus(), which a headless gate
// container does not reliably grant, so pin it true.
async function forceDocumentFocused(page: import('@playwright/test').Page): Promise<void> {
  await page.evaluate(() => {
    Object.defineProperty(document, 'hasFocus', { value: () => true, configurable: true });
  });
}

// Opens the popover and pushes by keyboard with the pointer parked outside,
// so a hover-hold cannot cancel the timer under test.
async function openAndWhip(app: import('../../../pages/index.js').AppShell): Promise<void> {
  await app.titlebar.openWhipPanel();
  await app.page.mouse.move(0, 0);
  await app.titlebar.whipRunButton().press('Enter');
  await app.titlebar.waitForWhipReportOpen();
}

test.describe('whip report auto-dismiss', () => {
  test('an all-pushed report dismisses itself after the transient duration', async ({ app }) => {
    await mockWhipReport(app.page, [
      { kind: 'environment', id: 'pw/alpha', name: 'pw/alpha', outcome: 'pushed' },
    ]);
    await app.page.clock.install();
    await forceDocumentFocused(app.page);

    await openAndWhip(app);
    await expect(app.titlebar.whipReportBody().getByText('Pushed', { exact: true })).toBeVisible();

    await app.page.clock.fastForward(TRANSIENT_DISMISS_MS + 200);
    await app.titlebar.waitForWhipReportClosed();
    await expect(app.titlebar.whipReportHeading()).toBeHidden();
  });

  test('a report containing a capped, failed, or skipped row stays open indefinitely', async ({
    app,
  }) => {
    await mockWhipReport(app.page, [
      { kind: 'environment', id: 'pw/alpha', name: 'pw/alpha', outcome: 'pushed' },
      { kind: 'orchestrator', id: 'pw-orch', name: 'pw-orch', outcome: 'capped' },
    ]);
    await app.page.clock.install();
    await forceDocumentFocused(app.page);

    await openAndWhip(app);
    await expect(app.titlebar.whipReportBody().getByText('Capped', { exact: true })).toBeVisible();

    await app.page.clock.fastForward(TRANSIENT_DISMISS_MS * 3);
    await expect(app.titlebar.whipReportHeading()).toBeVisible();
  });

  test('an empty ("nothing was targeted") report is not treated as a success', async ({ app }) => {
    await mockWhipReport(app.page, []);
    await app.page.clock.install();
    await forceDocumentFocused(app.page);

    await openAndWhip(app);
    await expect(app.titlebar.whipReportBody().getByText('Nothing was targeted')).toBeVisible();

    await app.page.clock.fastForward(TRANSIENT_DISMISS_MS * 3);
    await expect(app.titlebar.whipReportHeading()).toBeVisible();
  });

  test('hovering the report holds the timer until the pointer leaves', async ({ app }) => {
    await mockWhipReport(app.page, [
      { kind: 'environment', id: 'pw/alpha', name: 'pw/alpha', outcome: 'pushed' },
    ]);
    await app.page.clock.install();
    await forceDocumentFocused(app.page);

    await openAndWhip(app);
    const heading = app.titlebar.whipReportHeading();
    await expect(app.titlebar.whipReportBody().getByText('Pushed', { exact: true })).toBeVisible();

    await app.titlebar.whipReportBody().hover();
    await app.page.clock.fastForward(TRANSIENT_DISMISS_MS * 2);
    await expect(heading).toBeVisible();

    // The timer restarts from the leave, not the report's arrival.
    await app.page.mouse.move(0, 0);
    await app.page.clock.fastForward(TRANSIENT_DISMISS_MS + 200);
    await app.titlebar.waitForWhipReportClosed();
    await expect(heading).toBeHidden();
  });

  test('a second whip issued after reopening does not inherit a stale timer', async ({ app }) => {
    await mockWhipReport(app.page, [
      { kind: 'environment', id: 'pw/alpha', name: 'pw/alpha', outcome: 'pushed' },
    ]);
    await app.page.clock.install();
    await forceDocumentFocused(app.page);

    await openAndWhip(app);
    await expect(app.titlebar.whipReportBody().getByText('Pushed', { exact: true })).toBeVisible();

    // Reopen before the first timer would fire. The close animation runs on the
    // real clock, so wait for it to finish or the reopen races the unmount.
    await app.titlebar.closeWhipReport();
    await expect(app.titlebar.whipReportHeading()).toBeHidden();
    await app.page.clock.fastForward(TRANSIENT_DISMISS_MS - 500);

    await openAndWhip(app);
    await expect(app.titlebar.whipReportBody().getByText('Pushed', { exact: true })).toBeVisible();
    await app.page.clock.fastForward(600);
    // A surviving stale timer would have fired by now.
    await expect(app.titlebar.whipReportHeading()).toBeVisible();
  });
});
