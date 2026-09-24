import type { Page } from '@playwright/test';

import { boundingBoxOf } from '../fixtures/boundingBox.js';
import { expect, test } from '../fixtures/erunApp.js';

// Regression coverage for the titlebar's status pill and its
// surrounding flex rows (Titlebar.tsx, Titlebar.Status.tsx) are `flex`
// containers whose children default to `min-width: auto`. An unbroken
// status string with no `min-w-0` anywhere above it refuses to shrink below
// its own content width, dragging the titlebar row wider than the viewport
// and pushing the dismiss button off-screen -- exactly the mechanism the
// code comment at Titlebar.tsx's root div describes. This is one of the
// four independently-filed-and-fixed instances of this defect; the
// message-centre escalation path (titlebar-status-overflow.spec.ts) covers
// the *long* (>160 char, popover-escalated) case, not this one -- a message
// under that threshold stays in the inline pill this spec targets.

// One unbroken token, no spaces to wrap on, comfortably under
// LONG_STATUS_THRESHOLD (160) so it stays the inline tooltip pill rather
// than escalating to the popover.
const UNBROKEN_STATUS = 'aws-cloudformation-stack-update-in-progress-us-east-1-' + 'x'.repeat(90);

async function emitAppStatus(page: Page, message: string): Promise<void> {
  await page.evaluate((msg) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('app-status', { message: msg, busy: false });
  }, message);
}

async function hasHorizontalOverflow(page: Page): Promise<boolean> {
  return page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  );
}

for (const width of [480, 640, 900, 1440]) {
  test.describe(`titlebar status flex overflow at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } });

    test(`an unbroken status string does not widen the titlebar past ${width}px`, async ({
      app,
      page,
    }) => {
      expect(UNBROKEN_STATUS.length).toBeLessThan(160);
      expect(UNBROKEN_STATUS).not.toContain(' ');

      const dismiss = page.getByRole('button', { name: 'Dismiss status' });

      // One re-drivable block, with the emit inside it, because the pill is not
      // a stable surface: output resuming on the active session clears it
      // (hideTerminalMessageIfActive, dispatched from TerminalController on
      // every terminal-output event), and the default environment this harness
      // auto-opens on every boot is still streaming when app.open() returns.
      //
      // Emitting once and then walking the pill across separate steps is what
      // this spec used to do, and it reddened under a contended gate every time
      // the pill was cleared between two of them: the step that followed waited
      // for a dismiss control that was not coming back, and the test's own 30s
      // clock expired before the wait did -- reported at 640px and 1440px in
      // one full-suite run and at 480px in a repeat-each run, never on a quiet
      // one. Every step inside the block is bounded well below the test's
      // clock, so a dropped pill costs one attempt instead of the whole test,
      // the same shape erun-ui/playwright/AGENTS.md prescribes for the
      // hover cards' own dropped-and-not-reopened surface.
      await expect(async () => {
        await emitAppStatus(page, UNBROKEN_STATUS);
        await expect(app.titlebar.statusMessage()).toContainText(UNBROKEN_STATUS, {
          timeout: 2_000,
        });
        // waitFor, not expect(...).toBeVisible(): this is a state transition,
        // and the assertion's own 10s budget is a second, much tighter clock
        // than the test's.
        await dismiss.waitFor({ state: 'visible', timeout: 2_000 });

        expect(await hasHorizontalOverflow(page)).toBe(false);

        // Bounded: an unbounded boundingBox() waits for the element until the
        // test's clock expires, which inside a re-drivable block is the whole
        // budget spent on one attempt rather than a retry.
        const box = await boundingBoxOf(dismiss, `Dismiss status button at ${width}px`, 2_000);
        expect(box.x).toBeGreaterThanOrEqual(0);
        expect(box.x + box.width).toBeLessThanOrEqual(width);
      }).toPass();

      await app.titlebar.dismissStatus();
    });
  });
}

// The contended red the four cases above took, forced on demand instead of
// waited for.
//
// The pill is not a stable surface: output resuming on the active session
// clears it (hideTerminalMessageIfActive, dispatched from TerminalController on
// every terminal-output event), and this harness auto-opens a default
// environment on every boot whose session is still streaming when app.open()
// returns. Emitting once and then walking the pill across separate steps
// therefore loses it to traffic the spec never asked for, and the step that
// followed waited for a dismiss control that was not coming back -- the four
// failures seen under contention were all at that step (a bare
// locator.boundingBox() or locator.click() against the test's own 30s clock,
// with the pill's own visibility check just before it having passed), at
// 640px and 1440px in one full-suite run and at 480px in a repeat-each one,
// never on a quiet node.
//
// The clear is forced here rather than waited for, so the case is decided on a
// quiet machine too. The drop is delivered the way the app delivers it -- real
// output on the session the pill belongs to -- so what converges afterwards is
// the spec's own re-emit, not a wider number.
test('the status pill is re-established when the active session clears it', async ({
  app,
  page,
}) => {
  const dismiss = page.getByRole('button', { name: 'Dismiss status' });

  // The pill is up with this spec's own message...
  await emitAppStatus(page, UNBROKEN_STATUS);
  await expect(app.titlebar.statusMessage()).toContainText(UNBROKEN_STATUS, { timeout: 2_000 });

  // ... and then the app clears it, the way the boot flow's own streaming does
  // under contention. Empty output is enough: the handler only checks that the
  // event came from the active session and that a message is showing.
  await app.terminalPane.emitOutput(await app.terminalPane.selectedSessionId(), '');

  // Everything below is the point of the case. The four viewport tests above
  // read the pill straight through from here, which is what lost it under
  // contention; this block re-establishes it per attempt instead.
  await expect(async () => {
    await emitAppStatus(page, UNBROKEN_STATUS);
    await expect(app.titlebar.statusMessage()).toContainText(UNBROKEN_STATUS, { timeout: 2_000 });
    await dismiss.waitFor({ state: 'visible', timeout: 2_000 });

    expect(await hasHorizontalOverflow(page)).toBe(false);
    const box = await boundingBoxOf(dismiss, 'Dismiss status button after a clear', 2_000);
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(1440);
  }).toPass();

  await app.titlebar.dismissStatus();
});
