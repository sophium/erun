import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';
import { ManageDialog } from '../pages/ManageDialog.js';
import type { Page } from '@playwright/test';

// The runtime-unreachable warning used to linger while a deploy for that env was
// already in flight, and stay up after the runtime came back — contradicting the
// deploy-progress overlay. The Go side now tags the warning with its env and fires
// an `app-notification-clear` when that state moves on; a matching clear must dismiss
// the warning while a mismatched one must not. The Go decisions that fire these
// events are covered by env_ensure_test.go and activity_queue_app_test.go.
test.describe('runtime-unreachable banner clears with the deploy lifecycle (#713)', () => {
  const message =
    'Could not reach the runtime for frs/prod: runtime for frs/prod is not deployed. Deploy the environment to bring it up.';
  const banner = (page: Page) => page.getByText(/Could not reach the runtime for frs\/prod/);

  test('a matching clear dismisses the warning; a mismatched one does not', async ({ app }) => {
    const { page } = app;
    await emitRuntimeUnreachable(page, message);
    await expect(banner(page)).toBeVisible();

    // Guards against the Go side emitting an unrecognized kind (e.g. "warn") that
    // falls through to the neutral info icon instead of the warning attention icon.
    expect(await bannerIconKind(page)).toBe('alert');

    // A clear for a different env must NOT dismiss this warning; sampling over a
    // window catches a buggy "clear everything" once its async SSE clear lands.
    await emitNotificationClear(page, {
      tenant: 'other',
      environment: 'prod',
      source: 'runtime-unreachable',
    });
    expect(await bannerHiddenWithin(page, 700)).toBe(false);

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
    // A real runtime-unreachable message is far longer than the pill; it used to
    // stretch the header past the viewport so nothing truncated and the dismiss X
    // was pushed off-screen, leaving the banner un-dismissable.
    const longMessage =
      'Could not reach the runtime for frs/prod: runtime for frs/prod is not deployed ' +
      '(deployment "frs-devops" not found in namespace "frs-prod"); run `erun deploy frs prod` first. ' +
      'Deploy the environment to bring it up.';
    await emitRuntimeUnreachable(page, longMessage);
    await expect(banner(page)).toBeVisible();

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

    await dismiss.click();
    await expect(banner(page)).toBeHidden();
  });

  // #1390: the message names "Deploy the environment" as its fix, and the
  // app already has a control for that — the Manage dialog's Runtime tab,
  // where the operator picks a version and clicks Deploy. The banner must
  // offer that control directly rather than leaving the operator to find it
  // themselves. Backed by a real seeded env (not the fabricated frs/prod
  // used above) so opening the Manage dialog resolves real config instead of
  // erroring on an unknown tenant/environment. The orchestrator's own
  // "deploy or reopen that environment" edge-unreachable notice (#1390,
  // wireOrchestratorMCP) renders through this exact same action field and
  // component, covered on the Go side by
  // TestWireOrchestratorMCPWiresAnUnreachableEnvAndSaysSo; this is the one
  // rendering path both share.
  test('a "Deploy" action opens the Manage dialog straight to Runtime, and dismisses the banner', async ({
    app,
  }) => {
    const { page } = app;
    const message = `Could not reach the runtime for ${SEED_TENANT}/${SEED_ENV_ALPHA}: timed out. Deploy the environment to bring it up.`;
    await page.evaluate(
      ({ msg, tenant, environment }) => {
        (window as unknown as RuntimeShim).runtime.EventsEmit('app-notification', {
          kind: 'warning',
          message: msg,
          tenant,
          environment,
          source: 'runtime-unreachable',
          action: 'deploy',
        });
      },
      { msg: message, tenant: SEED_TENANT, environment: SEED_ENV_ALPHA },
    );

    const deployBanner = page
      .getByRole('status')
      .filter({ hasText: 'Could not reach the runtime' });
    await expect(deployBanner).toBeVisible();
    // exact: true — the message itself contains the substring "Deploy" (the
    // long-message trigger's accessible name is the whole sentence), which
    // would otherwise also match this query.
    const deployAction = deployBanner.getByRole('button', { name: 'Deploy', exact: true });
    await expect(deployAction).toBeVisible();
    await deployAction.click();

    const dialog = new ManageDialog(page, `${SEED_TENANT}-${SEED_ENV_ALPHA}`);
    await dialog.waitForOpen();
    await expect.poll(() => dialog.getActiveTab()).toBe('Runtime');
    await expect(deployBanner).toBeHidden();
  });

  // A message with no unambiguous env to target (the orchestrator's own
  // multi-env case, or any other warning) must render with no Deploy
  // control — manufacturing one would offer a click that cannot know which
  // environment to open.
  test('a runtime-unreachable-shaped message with no deploy action offers no Deploy button', async ({
    app,
  }) => {
    const { page } = app;
    const message = `Could not reach the runtime for ${SEED_TENANT}/${SEED_ENV_ALPHA}: timed out. Deploy the environment to bring it up.`;
    await page.evaluate(
      ({ msg, tenant, environment }) => {
        (window as unknown as RuntimeShim).runtime.EventsEmit('app-notification', {
          kind: 'warning',
          message: msg,
          tenant,
          environment,
          source: 'runtime-unreachable',
        });
      },
      { msg: message, tenant: SEED_TENANT, environment: SEED_ENV_ALPHA },
    );

    const banner = page.getByRole('status').filter({ hasText: 'Could not reach the runtime' });
    await expect(banner).toBeVisible();
    await expect(banner.getByRole('button', { name: 'Deploy', exact: true })).toHaveCount(0);
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

// bannerHiddenWithin is the deterministic "assert it stayed" primitive for the
// mismatched-clear case: a real (async SSE) clear would land within the window, and
// the in-browser poll is a bounded observation, not a banned spec-side sleep.
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
