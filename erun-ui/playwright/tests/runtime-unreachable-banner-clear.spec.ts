import { test, expect } from '../fixtures/erunApp.js';
import type { Page } from '@playwright/test';

// Issue #713 — the "Could not reach the runtime … Deploy the environment to
// bring it up." warning (issue #711) used to linger while a deploy for that env
// was already in flight, contradicting the deploy-progress overlay, and stayed
// up after the runtime became reachable again. The Go side now tags the warning
// with its env + a stable source and fires an `app-notification-clear` when the
// state it describes moves on (a deploy for the env starts, or the runtime is
// reached). This spec locks the frontend contract that backs that clear: a
// matching clear dismisses the warning, a mismatched one leaves it alone.
//
// The events are the same ones the Go side emits (app-notification /
// app-notification-clear), injected over the EventsEmit seam, so no cluster or
// real reconnect is needed. The Go decisions that fire these events are covered
// by env_ensure_test.go and activity_queue_app_test.go.
test.describe('runtime-unreachable banner clears with the deploy lifecycle (#713)', () => {
  const message =
    'Could not reach the runtime for frs/prod: timed out waiting for API port-forward. Deploy the environment to bring it up.';
  const banner = (page: Page) => page.getByText(/Could not reach the runtime for frs\/prod/);

  test('a matching clear dismisses the warning; a mismatched one does not', async ({ app }) => {
    // The `app` fixture boots the headless app (navigates to '/' and installs
    // the window.runtime shim the injected events ride on).
    const { page } = app;
    await emitRuntimeUnreachable(page, message);
    await expect(banner(page)).toBeVisible();

    // A clear for a different env must NOT dismiss this warning. Sample over a
    // window (mirrors terminal-scroll-on-resize's negative check) so a buggy
    // "clear everything" would be caught once its SSE round-trip lands.
    await emitNotificationClear(page, {
      tenant: 'other',
      environment: 'prod',
      source: 'runtime-unreachable',
    });
    expect(await bannerHiddenWithin(page, 700)).toBe(false);

    // The matching clear — what the deploy lifecycle fires when a deploy for
    // frs/prod starts or the runtime is reached — dismisses it.
    await emitNotificationClear(page, {
      tenant: 'frs',
      environment: 'prod',
      source: 'runtime-unreachable',
    });
    await expect(banner(page)).toBeHidden();
  });
});

interface RuntimeShim {
  runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
}

async function emitRuntimeUnreachable(page: Page, message: string): Promise<void> {
  await page.evaluate((msg) => {
    (window as unknown as RuntimeShim).runtime.EventsEmit('app-notification', {
      kind: 'warn',
      message: msg,
      tenant: 'frs',
      environment: 'prod',
      source: 'runtime-unreachable',
    });
  }, message);
}

async function emitNotificationClear(
  page: Page,
  target: { tenant: string; environment: string; source: string },
): Promise<void> {
  await page.evaluate((t) => {
    (window as unknown as RuntimeShim).runtime.EventsEmit('app-notification-clear', t);
  }, target);
}

// bannerHiddenWithin samples the banner's presence for `ms` and reports whether
// it ever went hidden — the deterministic "assert it stayed" primitive for the
// mismatched-clear case (an async SSE clear would land within the window). The
// sampling loop runs in the browser (mirrors terminal-scroll-on-resize's
// viewportEverAtBottom) so it is a bounded observation, not a spec-side sleep.
async function bannerHiddenWithin(page: Page, ms: number): Promise<boolean> {
  return await page.evaluate(async (duration) => {
    const present = (): boolean =>
      document.body.innerText.includes('Could not reach the runtime for frs/prod');
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
