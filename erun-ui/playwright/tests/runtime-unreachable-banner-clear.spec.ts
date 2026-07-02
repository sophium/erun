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

    // The warning renders with the attention icon (lucide circle-alert), not the
    // neutral info ⓘ — the Go side must emit kind "warning", not an unrecognized
    // "warn" that falls through to the info icon (#713).
    expect(await bannerIconKind(page)).toBe('alert');

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

  test('a long message stays within the bar and the dismiss button is reachable', async ({
    app,
  }) => {
    const { page } = app;
    // A real runtime-unreachable message includes the port-forward log path and
    // is far longer than the pill — it used to stretch the header past the
    // viewport, so nothing truncated and the dismiss X was pushed off-screen,
    // leaving the banner un-dismissable (#713).
    const longMessage =
      'Could not reach the runtime for frs/prod: activate MCP port-forward: exit status 1: ' +
      'timed out waiting for API port-forward on 127.0.0.1:17333; see ' +
      '/Users/example/Library/Application Support/erun/portforward/api/frs/prod.log. ' +
      'Deploy the environment to bring it up.';
    await emitRuntimeUnreachable(page, longMessage);
    await expect(banner(page)).toBeVisible();

    // The header must not overflow the viewport, and the dismiss button must sit
    // fully inside it (the bug pushed it hundreds of px off the right edge).
    const viewportWidth = page.viewportSize()?.width ?? 0;
    const headerRight = await page.evaluate(
      () =>
        document.querySelector('header')?.getBoundingClientRect().right ?? Number.POSITIVE_INFINITY,
    );
    expect(headerRight).toBeLessThanOrEqual(viewportWidth + 1);

    const dismiss = page.getByRole('button', { name: 'Dismiss status' });
    const box = await dismiss.boundingBox();
    expect(box).not.toBeNull();
    expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(viewportWidth);

    // And it actually dismisses — the whole point.
    await dismiss.click();
    await expect(banner(page)).toBeHidden();
  });
});

interface RuntimeShim {
  runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
}

async function emitRuntimeUnreachable(page: Page, message: string): Promise<void> {
  await page.evaluate((msg) => {
    (window as unknown as RuntimeShim).runtime.EventsEmit('app-notification', {
      kind: 'warning',
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

// bannerIconKind reads the icon lucide renders inside the runtime-unreachable
// banner: 'alert' for the attention icon (circle-alert, warning/error), 'info'
// for the neutral ⓘ, or 'other'. Locks that the warning gets the attention icon.
async function bannerIconKind(page: Page): Promise<'alert' | 'info' | 'other'> {
  return await page.evaluate(() => {
    const banners = Array.from(document.querySelectorAll('[role="status"],[role="alert"]'));
    const banner = banners.find((b) =>
      (b.textContent ?? '').includes('Could not reach the runtime for frs/prod'),
    );
    const cls = banner?.querySelector('svg')?.getAttribute('class') ?? '';
    if (cls.includes('lucide-circle-alert')) {
      return 'alert';
    }
    if (cls.includes('lucide-info')) {
      return 'info';
    }
    return 'other';
  });
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
