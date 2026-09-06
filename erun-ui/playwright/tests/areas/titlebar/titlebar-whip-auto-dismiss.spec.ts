import type { Request, Route } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// A fully successful whip push auto-dismisses its report popover
// after the app's established transient duration (instead of sitting open
// until the operator clicks it away) — a push that did NOT fully succeed must
// stay open, since its outcome is the only place that gets reported at all.
// TRANSIENT_DISMISS_MS lives in erun-ui/frontend/src/app/transientDismissDuration.ts;
// this suite is a separate package that drives the app only through the DOM
// and RPC bridge, so the value is duplicated here rather than imported —
// keep the two in lockstep if that constant ever changes.
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

// scheduleTransientDismiss (app/transientDismissTimer.ts) only arms its
// timer while document.hasFocus() is true -- deliberate, so a toast never
// silently marks itself seen while the operator is in another window. That
// pause/resume-on-blur behaviour already has full coverage in
// transientDismissTimer.test.ts's jsdom unit tests; what these specs need is
// just the "window is focused" precondition to hold, so the fake clock's
// fastForward has an armed timer to advance. page.bringToFront() used to
// supply that by asking the OS/window manager for real focus, but a headless
// gate container does not reliably grant it -- confirmed by a real 'erun
// build' run failing here while every standalone run.sh pass went green.
// Pin document.hasFocus() to true directly instead of hoping the browser
// wins real focus.
async function forceDocumentFocused(page: import('@playwright/test').Page): Promise<void> {
  await page.evaluate(() => {
    Object.defineProperty(document, 'hasFocus', { value: () => true, configurable: true });
  });
}

// Opens the popover and pushes, landing on the report view (skipping past
// the target picker every one of these tests starts from). Clicking the
// primary action leaves the mouse resting on the popover -- exactly like a
// real operator's cursor would -- so this moves it away afterward. Without
// that, `hovered` stays true from the click itself and the auto-dismiss
// timer (which pauses while hovered) never starts at all, which is the
// pause/resume feature working correctly, not something to route around.
async function openAndWhip(app: import('../../../pages/index.js').AppShell): Promise<void> {
  await app.titlebar.whipButton().click();
  await app.titlebar.whipRunButton().click();
  await app.page.mouse.move(0, 0);
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

    // Move off the popover -- the timer restarts from here, not from the
    // original report arrival.
    await app.page.mouse.move(0, 0);
    await app.page.clock.fastForward(TRANSIENT_DISMISS_MS + 200);
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

    // Close and reopen well before the first report's timer would have
    // fired -- reopening clears any leftover timer, so a fresh whip's own
    // report gets its own full window. Reopening returns to the target
    // picker (nothing carries over between opens), so a second whip needs
    // its own click to produce a new report. The close animation runs on the
    // real browser clock, not the fake one the test controls, so the popover
    // must be confirmed fully gone before reopening -- clicking through a
    // still-closing Radix Presence exit leaves the reopen's own onOpenChange
    // racing that unmount, and the popover never comes back.
    await app.titlebar.closeWhipReport();
    await expect(app.titlebar.whipReportHeading()).toBeHidden();
    await app.page.clock.fastForward(TRANSIENT_DISMISS_MS - 500);

    await openAndWhip(app);
    await expect(app.titlebar.whipReportBody().getByText('Pushed', { exact: true })).toBeVisible();
    await app.page.clock.fastForward(600);
    // The stale timer (had it survived) would have fired by now; the report
    // must still be showing because its own timer only just started.
    await expect(app.titlebar.whipReportHeading()).toBeVisible();
  });
});
