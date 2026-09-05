import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';
import { ManageDialog } from '../pages/ManageDialog.js';
import type { Page } from '@playwright/test';

// The runtime-unreachable warning used to linger while a deploy for that env was
// already in flight, and stay up after the runtime came back — contradicting the
// deploy-progress overlay. The Go side tags the warning with its env and fires
// an `app-notification-clear` when that state moves on; a matching clear must
// mark the warning read while a mismatched one must not (dismissal never
// deletes a message, only its unread state — the warning icon's count
// is what "cleared" now means). The Go decisions that fire these events are
// covered by env_ensure_test.go and activity_queue_app_test.go.
test.describe('runtime-unreachable banner clears with the deploy lifecycle (#713)', () => {
  const message =
    'Could not reach the runtime for frs/prod: runtime for frs/prod is not deployed. Deploy the environment to bring it up.';

  test('a matching clear marks the warning read; a mismatched one does not', async ({ app }) => {
    const { page } = app;
    await emitRuntimeUnreachable(page, message);

    // Guards against the Go side emitting an unrecognized kind (e.g. "warn")
    // that falls through to the neutral info icon instead of the warning one.
    const warningIcon = app.titlebar.messageCenterIcon('warning');
    await expect(warningIcon).toHaveAccessibleName('Warning: 1 unread');
    await expect(app.titlebar.messageCenterIcon('info')).toHaveCount(0);

    // A clear for a different env must NOT mark this one read; sampling over
    // a window catches a buggy "clear everything" once its async SSE clear
    // lands.
    await emitNotificationClear(page, {
      tenant: 'other',
      environment: 'prod',
      source: 'runtime-unreachable',
    });
    expect(await stillUnreadWithin(page, 'Warning: 1 unread', 700)).toBe(true);

    await emitNotificationClear(page, {
      tenant: 'frs',
      environment: 'prod',
      source: 'runtime-unreachable',
    });
    await expect(warningIcon).toHaveCount(0);
  });

  // The message centre retired the always-inline pill this originally
  // guarded (a long notification used to stretch the titlebar header past
  // the viewport, pushing its dismiss button off-screen). Notification-channel messages no
  // longer render inline at all -- they live in the message centre dialog,
  // whose own max-width/overflow handling (DialogContent's sm:max-w-2xl,
  // whitespace-pre-wrap break-words rows) is a fixed-size modal rather than a
  // width the header has to accommodate, so this specific failure mode no
  // longer has a code path to regress through.

  // The message names "Deploy the environment" as its fix, and the
  // app already has a control for that — the Manage dialog's Runtime tab,
  // where the operator picks a version and clicks Deploy. The message centre
  // row must offer that control directly rather than leaving the operator to
  // find it themselves, and clicking it closes the dialog rather than
  // stacking a second modal underneath the one just opened. Backed by a real
  // seeded env (not the fabricated frs/prod used above) so opening the
  // Manage dialog resolves real config instead of erroring on an unknown
  // tenant/environment. The orchestrator's own "deploy or reopen that
  // environment" edge-unreachable notice (wireOrchestratorMCP) renders
  // through this exact same action field and component, covered on the Go
  // side by TestWireOrchestratorMCPWiresAnUnreachableEnvAndSaysSo; this is
  // the one rendering path both share.
  test('a "Deploy" action opens the Manage dialog straight to Runtime, and closes the message centre', async ({
    app,
  }) => {
    const { page } = app;
    const deployMessage = `Could not reach the runtime for ${SEED_TENANT}/${SEED_ENV_ALPHA}: timed out. Deploy the environment to bring it up.`;
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
      { msg: deployMessage, tenant: SEED_TENANT, environment: SEED_ENV_ALPHA },
    );

    await app.titlebar.openMessageCenter('warning');
    const row = app.titlebar.messageCenterRow('Could not reach the runtime');
    // exact: true — the message itself contains the substring "Deploy" (the
    // sentence names "Deploy the environment"), which would otherwise also
    // match this query.
    const deployAction = row.getByRole('button', { name: 'Deploy', exact: true });
    await expect(deployAction).toBeVisible();
    await deployAction.click();

    const dialog = new ManageDialog(page, `${SEED_TENANT}-${SEED_ENV_ALPHA}`);
    await dialog.waitForOpen();
    await expect.poll(() => dialog.getActiveTab()).toBe('Runtime');
    await expect(app.titlebar.messageCenterDialog()).toBeHidden();
  });

  // A message with no unambiguous env to target (the orchestrator's own
  // multi-env case, or any other warning) must render with no Deploy
  // control — manufacturing one would offer a click that cannot know which
  // environment to open.
  test('a runtime-unreachable-shaped message with no deploy action offers no Deploy button', async ({
    app,
  }) => {
    const { page } = app;
    const noActionMessage = `Could not reach the runtime for ${SEED_TENANT}/${SEED_ENV_ALPHA}: timed out. Deploy the environment to bring it up.`;
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
      { msg: noActionMessage, tenant: SEED_TENANT, environment: SEED_ENV_ALPHA },
    );

    await app.titlebar.openMessageCenter('warning');
    const row = app.titlebar.messageCenterRow('Could not reach the runtime');
    await expect(row).toBeVisible();
    await expect(row.getByRole('button', { name: 'Deploy', exact: true })).toHaveCount(0);
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

// The deterministic "assert it stayed unread" primitive: returns true only if
// the warning icon's accessible name still reports the unread count
// throughout the window. The polling loop runs inside page.evaluate (its own
// setTimeout), not page.waitForTimeout, so this stays a bounded in-browser
// observation rather than a spec-side sleep.
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
