import { test, expect } from '../fixtures/erunApp.js';
import type { Page } from '@playwright/test';

// A failed deploy must be visible in the top toolbar, not only as a red terminal
// line or drawer entry — pre-rollout failures (e.g. spec resolution) weren't
// surfaced at all. This spec locks the frontend contract: the deploy-failed error
// shows an unread error icon; the deploy lifecycle's env-wide clear (a new deploy
// starting, or the runtime becoming reachable) marks it read (dismissal never
// deletes a message, only its unread state), while a clear for a
// different env leaves it unread. The Go trace→surface wiring is covered by
// activity_queue_app_test.go::TestDeployFailedTraceSurfacesToToolbar.
test.describe('failed deploys surface in the toolbar (#713)', () => {
  const message = 'Deploy of frs/prod failed: values file not found for environment "prod".';

  test('the deploy-failed error is unread until the env-wide lifecycle clear marks it read', async ({
    app,
  }) => {
    const { page } = app;
    const icon = app.titlebar.messageCenterIcon('error');

    await emitDeployFailed(page, message);
    await expect(icon).toHaveAccessibleName('Error: 1 unread');

    // A lifecycle clear for a different env must NOT mark this one read.
    // Sample over a window so a buggy "clear everything" is caught once its
    // SSE lands.
    await emitClear(page, { tenant: 'other', environment: 'prod', source: '' });
    expect(await stillUnreadWithin(page, 'Error: 1 unread', 700)).toBe(true);

    // The env-wide clear for frs/prod (empty source = any env-scoped warning) marks it read.
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

    // Error notifications carry long paths the operator needs to copy into a
    // bug report, so every row gets a copy button like terminal-status
    // errors do.
    const copy = row.getByRole('button', { name: 'Copy', exact: true });
    await expect(copy).toBeVisible();

    // The headless shim routes the native clipboard write to an observable HTTP
    // POST, so the copy is asserted as a request, not by reading a real clipboard.
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

// The deterministic "assert it stayed unread" primitive: returns true only if
// the error icon's accessible name still reports the unread count throughout
// the window (mirrors terminal-scroll-on-resize's viewportEverAtBottom). The
// polling loop runs inside page.evaluate (its own setTimeout), not
// page.waitForTimeout, so this stays a bounded in-browser observation rather
// than a spec-side sleep.
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
