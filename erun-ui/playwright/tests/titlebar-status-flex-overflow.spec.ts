import type { Page } from '@playwright/test';

import { boundingBoxOf } from '../fixtures/boundingBox.js';
import { expect, test } from '../fixtures/erunApp.js';

// Regression coverage for #373: the titlebar's status pill and its
// surrounding flex rows (Titlebar.tsx, Titlebar.Status.tsx) are `flex`
// containers whose children default to `min-width: auto`. An unbroken
// status string with no `min-w-0` anywhere above it refuses to shrink below
// its own content width, dragging the titlebar row wider than the viewport
// and pushing the dismiss button off-screen -- exactly the mechanism the
// code comment at Titlebar.tsx's root div describes. This is one of the
// four independently-filed-and-fixed instances erun#2164 catalogues; the
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

      await emitAppStatus(page, UNBROKEN_STATUS);
      await expect(app.titlebar.statusMessage()).toBeVisible();

      await expect.poll(() => hasHorizontalOverflow(page)).toBe(false);

      const dismiss = page.getByRole('button', { name: 'Dismiss status' });
      await expect(dismiss).toBeVisible();
      const box = await boundingBoxOf(dismiss, `Dismiss status button at ${width}px`);
      expect(box.x).toBeGreaterThanOrEqual(0);
      expect(box.x + box.width).toBeLessThanOrEqual(width);

      await app.titlebar.dismissStatus();
    });
  });
}
