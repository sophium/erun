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
// the target picker every one of these tests starts from).
//
// The primary action is activated by KEYBOARD, deliberately. A click parks
// the pointer on the picker's primary action, and the report then replaces
// that picker underneath the cursor -- which raises PopoverContent's
// onMouseEnter (the hover-hold the timer pauses on) and, worse, leaves the
// outcome depending on where the pointer is when Chromium's own
// layout-driven hover update lands relative to the mouseleave asking for it
// to leave. `page.mouse.move(0, 0)` after the report appeared was the old way
// of asking for that mouseleave; it is not deterministic under the gate's
// contention, where the timer can be armed and cancelled again before the
// assertion below ever advances the clock, leaving an all-pushed report open
// forever. Keeping the pointer out of the popover for the whole flow removes
// that dependency: the timer is armed by the report's own mount, which is the
// thing under test. The hover-hold this routes around has its own test below,
// which drives the pointer into the report explicitly.
//
// The park happens BEFORE the push, not after the report appears: the whip
// button the panel was opened from is the popover's own anchor, so the click
// that opened it left the pointer on top of the surface that is about to
// change underneath it.
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

    // Move off the popover -- the timer restarts from here, not from the
    // original report arrival.
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
