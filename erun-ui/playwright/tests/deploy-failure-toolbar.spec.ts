import { test, expect } from '../fixtures/erunApp.js';
import type { Page } from '@playwright/test';

// Issue #713 — a failed deploy must be visible in the top toolbar, not only as a
// red terminal line or a drawer entry (it wasn't surfaced at all when the deploy
// failed before rollout, e.g. spec resolution). `erun deploy` now emits a
// `==> Deploy failed tenant/env: reason` trace on every failure path; the desktop
// turns that into an env-tagged error notification (surfaceDeployFailure). This
// spec locks the frontend contract: the deploy-failed error renders in the
// toolbar, and the deploy lifecycle's env-wide clear (empty source — a new
// deploy starting, or the runtime becoming reachable) retires it, while a clear
// for a different env leaves it alone.
//
// The events mirror what the Go side emits (app-notification with
// source=deploy-failed; app-notification-clear with an empty source), injected
// over the EventsEmit seam. The Go trace→surface wiring is covered by
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

    // The env-wide clear the deploy lifecycle fires for frs/prod (empty source =
    // any env-scoped warning) retires it — this is what a new deploy starting or
    // the runtime becoming reachable emits.
    await emitClear(page, { tenant: 'frs', environment: 'prod', source: '' });
    await expect(banner(page)).toBeHidden();
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

// bannerGoneWithin samples the error's presence for `ms` in the browser and
// reports whether it ever disappeared — the deterministic "assert it stayed"
// primitive (mirrors terminal-scroll-on-resize's viewportEverAtBottom).
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
