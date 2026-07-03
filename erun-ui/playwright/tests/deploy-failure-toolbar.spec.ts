import { test, expect } from '../fixtures/erunApp.js';
import type { Page } from '@playwright/test';

// A failed deploy must be visible in the top toolbar, not only as a red terminal
// line or drawer entry — pre-rollout failures (e.g. spec resolution) weren't
// surfaced at all. This spec locks the frontend contract: the deploy-failed error
// renders in the toolbar; the deploy lifecycle's env-wide clear (a new deploy
// starting, or the runtime becoming reachable) retires it, while a clear for a
// different env leaves it alone. The Go trace→surface wiring is covered by
// activity_queue_app_test.go::TestDeployFailedTraceSurfacesToToolbar.
test.describe('failed deploys surface in the toolbar (#713)', () => {
  const message = 'Deploy of frs/prod failed: values file not found for environment "prod".';
  const banner = (page: Page) => page.getByText(/Deploy of frs\/prod failed/);

  test('the deploy-failed error shows and is retired by the env-wide lifecycle clear', async ({
    app,
  }) => {
    const { page } = app;

    await emitDeployFailed(page, message);
    await expect(banner(page)).toBeVisible();

    // A lifecycle clear for a different env must NOT dismiss this error. Sample
    // over a window so a buggy "clear everything" is caught once its SSE lands.
    await emitClear(page, { tenant: 'other', environment: 'prod', source: '' });
    expect(await bannerGoneWithin(page, 700)).toBe(false);

    // The env-wide clear for frs/prod (empty source = any env-scoped warning) retires it.
    await emitClear(page, { tenant: 'frs', environment: 'prod', source: '' });
    await expect(banner(page)).toBeHidden();
  });

  test('the deploy-failed error can be copied to the clipboard', async ({ app }) => {
    const { page } = app;
    await emitDeployFailed(page, message);
    await expect(banner(page)).toBeVisible();

    // Error notifications carry long paths the operator needs to copy into a bug
    // report, so they get a copy button like terminal-status errors do.
    const copy = page.getByRole('button', { name: 'Copy', exact: true });
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

    await expect(page.getByRole('button', { name: 'Copied', exact: true })).toBeVisible();
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

// The deterministic "assert it stayed" primitive: returns true only if the error
// disappeared within the window (mirrors terminal-scroll-on-resize's
// viewportEverAtBottom).
async function bannerGoneWithin(page: Page, ms: number): Promise<boolean> {
  return await page.evaluate(async (duration) => {
    const present = (): boolean => document.body.innerText.includes('Deploy of frs/prod failed');
    const deadline = Date.now() + duration;
    while (Date.now() < deadline) {
      if (!present()) {
        return true;
      }
      await new Promise((resolve) => setTimeout(resolve, 25));
    }
    return false;
  }, ms);
}
