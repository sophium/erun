import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Class icons render in the titlebar's right-hand group, badges never rely on
// colour alone, and clearing marks read without deleting history.

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

// `idle <n>s` is the badge variant whose width tracks secondsUntilStop.
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

    // Right half of the fixed 1440px viewport.
    const boxBefore = await icon.boundingBox();
    expect(boxBefore).not.toBeNull();
    expect(boxBefore?.x ?? 0).toBeGreaterThan(720);

    // The icon is outboard of the idle label, so its x must not move.
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
    // State must not rest on colour alone, so the badge has its own background.
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

    await app.titlebar.openMessageCenter('error');
    await app.titlebar.messageCenterMarkClassReadButton('error').click();
    // The titlebar is aria-hidden while the modal is open.
    await app.titlebar.closeMessageCenter();

    await expect(errorIcon).toHaveCount(0);
    await expect(warningIcon).toBeVisible();

    await app.titlebar.openMessageCenter('warning');
    await app.titlebar.messageCenterMarkAllReadButton().click();
    await app.titlebar.closeMessageCenter();
    await expect(warningIcon).toHaveCount(0);

    // Cleared, not deleted. Another spec's unread notice may suppress the history
    // fallback, so go through whichever entry point is offered.
    await expect(app.titlebar.messageCenterEntryPoint()).toBeVisible();
    await app.titlebar.openMessageCenterFromTitlebar();
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

    await app.titlebar.openMessageCenter('error');
    await app.titlebar.messageCenterMarkAllReadButton().click();
    // The titlebar is aria-hidden while the modal is open.
    await app.titlebar.closeMessageCenter();

    await expect(errorIcon).toHaveCount(0);
    await expect(warningIcon).toHaveCount(0);
    await expect(app.titlebar.messageCenterEntryPoint()).toBeVisible();

    await app.titlebar.openMessageCenterFromTitlebar();
    await expect(app.titlebar.messageCenterRow('Combined probe: error.')).toBeVisible();
    await expect(app.titlebar.messageCenterRow('Combined probe: warning.')).toBeVisible();
  });
});
