import { test, expect } from '../../../fixtures/erunApp.js';
import type { Page } from '@playwright/test';

// Dismissal only marks a message read; it is never deleted.
test.describe('failed deploys surface in the toolbar (#713)', () => {
  const message = 'Deploy of frs/prod failed: values file not found for environment "prod".';

  test('the deploy-failed error is unread until the env-wide lifecycle clear marks it read', async ({
    app,
  }) => {
    const { page } = app;
    const icon = app.titlebar.messageCenterIcon('error');

    await emitDeployFailed(page, message);
    await expect(icon).toHaveAccessibleName('Error: 1 unread');

    // Sampled over a window so a buggy "clear everything" is caught.
    await emitClear(page, { tenant: 'other', environment: 'prod', source: '' });
    expect(await stillUnreadWithin(page, 'Error: 1 unread', 700)).toBe(true);

    // Empty source clears any env-scoped warning.
    await emitClear(page, { tenant: 'frs', environment: 'prod', source: '' });
    await expect(icon).toHaveCount(0);
  });

  test('the deploy-failed error can be copied to the clipboard from the message centre', async ({
    app,
  }) => {
    const { page } = app;
    await emitDeployFailed(page, message);
    await app.titlebar.openMessageCenter('error');
    const row = app.titlebar.messageCenterRow('Deploy of frs/prod failed');
    await expect(row).toBeVisible();

    const copy = row.getByRole('button', { name: 'Copy', exact: true });
    await expect(copy).toBeVisible();

    // The shim turns the clipboard write into an observable POST.
    const clipboardWrite = page.waitForRequest(
      (req) =>
        req.method() === 'POST' &&
        req.url().endsWith('/__erun_clipboard') &&
        (req.postData() ?? '').includes('"action":"set"'),
    );
    await copy.click();
    const req = await clipboardWrite;
    expect(req.postData() ?? '').toContain('Deploy of frs/prod failed');

    await expect(row.getByRole('button', { name: 'Copied', exact: true })).toBeVisible();
  });
});

interface RuntimeShim {
  runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
}

async function emitDeployFailed(page: Page, message: string): Promise<void> {
  await page.evaluate((msg) => {
    (window as unknown as RuntimeShim).runtime.EventsEmit('app-notification', {
      kind: 'error',
      message: msg,
      tenant: 'frs',
      environment: 'prod',
      source: 'deploy-failed',
    });
  }, message);
}

async function emitClear(
  page: Page,
  target: { tenant: string; environment: string; source: string },
): Promise<void> {
  await page.evaluate((t) => {
    (window as unknown as RuntimeShim).runtime.EventsEmit('app-notification-clear', t);
  }, target);
}

// Polls inside page.evaluate so it is a bounded in-browser observation, not a sleep.
async function stillUnreadWithin(page: Page, expectedLabel: string, ms: number): Promise<boolean> {
  return await page.evaluate(
    async ({ label, duration }) => {
      const present = (): boolean =>
        Array.from(document.querySelectorAll('button[aria-label]')).some(
          (el) => el.getAttribute('aria-label') === label,
        );
      const deadline = Date.now() + duration;
      while (Date.now() < deadline) {
        if (!present()) {
          return false;
        }
        await new Promise((resolve) => setTimeout(resolve, 25));
      }
      return true;
    },
    { label: expectedLabel, duration: ms },
  );
}
