import type { Page } from '@playwright/test';

import { boundingBoxOf } from '../fixtures/boundingBox.js';
import { expect, test } from '../fixtures/erunApp.js';

// Flex children default to `min-width: auto`, so an unbroken short status
// string could widen the titlebar and push the dismiss button off-screen.

// Unbroken and under LONG_STATUS_THRESHOLD (160), so it stays the inline pill.
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

      // Re-drivable with the emit inside: output on the auto-opened session clears
      // the pill, so a dropped pill costs one bounded attempt, not the whole test.
      await expect(async () => {
        await emitAppStatus(page, UNBROKEN_STATUS);
        await expect(app.titlebar.statusMessage()).toContainText(UNBROKEN_STATUS, {
          timeout: 2_000,
        });
        // waitFor: expect's own budget would be a second, tighter clock.
        await dismiss.waitFor({ state: 'visible', timeout: 2_000 });

        expect(await hasHorizontalOverflow(page)).toBe(false);

        // Bounded so one attempt cannot spend the whole test budget.
        const box = await boundingBoxOf(dismiss, `Dismiss status button at ${width}px`, 2_000);
        expect(box.x).toBeGreaterThanOrEqual(0);
        expect(box.x + box.width).toBeLessThanOrEqual(width);
      }).toPass();

      await app.titlebar.dismissStatus();
    });
  });
}

// Forces the clear (real output on the active session) instead of waiting for
// contention to cause it, so the re-establishing loop is exercised every run.
test('the status pill is re-established when the active session clears it', async ({
  app,
  page,
}) => {
  const dismiss = page.getByRole('button', { name: 'Dismiss status' });

  await emitAppStatus(page, UNBROKEN_STATUS);
  await expect(app.titlebar.statusMessage()).toContainText(UNBROKEN_STATUS, { timeout: 2_000 });

  // Empty output suffices: the handler only checks the session and a showing message.
  await app.terminalPane.emitOutput(await app.terminalPane.selectedSessionId(), '');

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
