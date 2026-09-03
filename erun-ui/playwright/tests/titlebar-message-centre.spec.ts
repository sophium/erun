import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The message centre's class icons must render in the titlebar's
// right-hand group (not the centred status slot), the unread badge must be
// legible and never rely on colour alone, and clearing a class (or
// everything) must mark it read without deleting it from history.

function emit(page: Page, payload: Record<string, unknown>): Promise<void> {
  return page.evaluate((notification) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('app-notification', notification);
  }, payload);
}

interface InvokeBody {
  method: string;
  args: unknown[];
}

function envelope(data: unknown): { contentType: string; body: string } {
  return { contentType: 'application/json', body: JSON.stringify({ data }) };
}

// A not-yet-eligible idle reading renders its label as `idle <n>s`
// (Titlebar.helpers.ts's idleStatusBadge) -- unlike the other badge
// variants ("idle ready", "outside hours"), this one's rendered width
// tracks the digit count of secondsUntilStop, which is exactly the
// variable-width case the layout fix needs to stay stable against.
function widthVaryingIdleStatus(secondsUntilStop: number): unknown {
  return {
    timeoutSeconds: 600,
    secondsUntilStop,
    stopEligible: false,
    outsideWorkingHours: false,
    managedCloud: true,
    fromPod: true,
    cloudContextName: 'mock-ctx-msg-centre',
    cloudContextStatus: 'running',
    cloudContextLabel: 'mock-ctx-msg-centre',
    markers: [],
  };
}

async function waitForNextIdlePoll(page: Page): Promise<void> {
  await page.waitForResponse(
    (response) =>
      response.url().includes('/__erun_invoke') &&
      (response.request().postData() ?? '').includes('"LoadIdleStatus"'),
  );
}

test.describe('titlebar message centre layout and bulk clear', () => {
  test('class icons stay right-aligned and hold position as the idle widget label changes width', async ({
    app,
    page,
  }) => {
    let secondsUntilStop = 5;
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'LoadIdleStatus') {
        return route.fulfill(envelope(widthVaryingIdleStatus(secondsUntilStop)));
      }
      if (body.method === 'DescribeCloudContextApiStop') {
        return route.fulfill(
          envelope({
            name: 'mock-ctx-msg-centre',
            stopProtection: false,
            stopProtectionKnown: true,
          }),
        );
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);
    const idleBadge = app.titlebar.idleStatusBadge();
    await expect(idleBadge).toHaveText('idle 5s');

    await emit(page, { kind: 'error', message: 'Layout probe error.' });
    const icon = app.titlebar.messageCenterIcon('error');
    await expect(icon).toBeVisible();

    // Right-aligned: the icon sits in the right half of a 1440px-wide viewport
    // (playwright.config.ts's fixed viewport), not the centred status slot.
    const boxBefore = await icon.boundingBox();
    expect(boxBefore).not.toBeNull();
    expect(boxBefore?.x ?? 0).toBeGreaterThan(720);

    // Grow the idle label from "idle 5s" to "idle 359999s" -- outboard
    // placement means the icon's absolute x must not move.
    secondsUntilStop = 359999;
    await waitForNextIdlePoll(page);
    await expect(idleBadge).toHaveText('idle 359999s');

    const boxAfter = await icon.boundingBox();
    expect(boxAfter).not.toBeNull();
    expect(Math.abs((boxAfter?.x ?? 0) - (boxBefore?.x ?? 0))).toBeLessThan(1);
  });

  test('unread badge is legible: distinct from the icon colour, and clamps at 9+', async ({
    app,
    page,
  }) => {
    await emit(page, { kind: 'error', message: 'Badge legibility probe 1.' });
    const icon = app.titlebar.messageCenterIcon('error');
    const badge = app.titlebar.messageCenterIconBadge('error');
    await expect(badge).toHaveText('1');

    const [badgeBackground, iconColor] = await Promise.all([
      badge.evaluate((el) => getComputedStyle(el).backgroundColor),
      icon
        .locator('svg')
        .first()
        .evaluate((el) => getComputedStyle(el).color),
    ]);
    // The badge's own background must never equal the icon's currentColor --
    // that is exactly the red-on-red/amber-on-amber separation defect this
    // fixes (root AGENTS.md's StatusDotGlyph rule: state must not rest on
    // colour alone, so the badge needs a fixed, independent background).
    expect(badgeBackground).not.toBe(iconColor);

    for (let i = 2; i <= 12; i++) {
      await emit(page, { kind: 'error', message: `Badge legibility probe ${String(i)}.` });
    }
    await expect(badge).toHaveText('9+');
    await expect(icon).toHaveAccessibleName('Error: 12 unread');
  });

  test('Mark <class> read zeroes only that class; Mark all read zeroes every class', async ({
    app,
    page,
  }) => {
    await emit(page, { kind: 'error', message: 'Scoped clear: error message.' });
    await emit(page, { kind: 'warning', message: 'Scoped clear: warning message.' });

    const errorIcon = app.titlebar.messageCenterIcon('error');
    const warningIcon = app.titlebar.messageCenterIcon('warning');
    await expect(errorIcon).toBeVisible();
    await expect(warningIcon).toBeVisible();

    await errorIcon.click();
    await expect(app.titlebar.messageCenterDialog()).toBeVisible();
    await app.titlebar.messageCenterMarkClassReadButton('error').click();
    // The rest of the titlebar is aria-hidden while the modal dialog is
    // open, so it must close before any titlebar-icon locator is trusted.
    await app.titlebar.closeMessageCenter();

    // Scoped to the error tab: the error icon disappears, the warning one
    // must not be touched.
    await expect(errorIcon).toHaveCount(0);
    await expect(warningIcon).toBeVisible();

    await warningIcon.click();
    await app.titlebar.messageCenterMarkAllReadButton().click();
    await app.titlebar.closeMessageCenter();
    await expect(warningIcon).toHaveCount(0);

    // Cleared, not deleted: the history fallback replaces the (now empty)
    // icon row, and both messages are still listed in the dialog.
    await expect(app.titlebar.messageCenterHistoryButton()).toBeVisible();
    await app.titlebar.messageCenterHistoryButton().click();
    await expect(app.titlebar.messageCenterRow('Scoped clear: error message.')).toBeVisible();
    await expect(app.titlebar.messageCenterRow('Scoped clear: warning message.')).toBeVisible();
  });

  test('raising an error and a warning shows two right-aligned badged icons; clearing all hides them but keeps both in the dialog', async ({
    app,
    page,
  }) => {
    await emit(page, { kind: 'error', message: 'Combined probe: error.' });
    await emit(page, { kind: 'warning', message: 'Combined probe: warning.' });

    const errorIcon = app.titlebar.messageCenterIcon('error');
    const warningIcon = app.titlebar.messageCenterIcon('warning');
    await expect(errorIcon).toBeVisible();
    await expect(warningIcon).toBeVisible();
    await expect(app.titlebar.messageCenterIconBadge('error')).toHaveText('1');
    await expect(app.titlebar.messageCenterIconBadge('warning')).toHaveText('1');

    const errorBox = await errorIcon.boundingBox();
    const warningBox = await warningIcon.boundingBox();
    expect(errorBox?.x ?? 0).toBeGreaterThan(720);
    expect(warningBox?.x ?? 0).toBeGreaterThan(720);

    await errorIcon.click();
    await app.titlebar.messageCenterMarkAllReadButton().click();
    // The rest of the titlebar is aria-hidden while the modal dialog is
    // open, so it must close before any titlebar-icon locator is trusted.
    await app.titlebar.closeMessageCenter();

    await expect(errorIcon).toHaveCount(0);
    await expect(warningIcon).toHaveCount(0);
    await expect(app.titlebar.messageCenterHistoryButton()).toBeVisible();

    await app.titlebar.messageCenterHistoryButton().click();
    await expect(app.titlebar.messageCenterRow('Combined probe: error.')).toBeVisible();
    await expect(app.titlebar.messageCenterRow('Combined probe: warning.')).toBeVisible();
  });
});
