import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';
import { ManageDialog } from '../../../pages/ManageDialog.js';
import type { Page } from '@playwright/test';

// A matching `app-notification-clear` marks the env's warning read; a mismatched one must not.
test.describe('runtime-unreachable banner clears with the deploy lifecycle (#713)', () => {
  const message =
    'Could not reach the runtime for frs/prod: runtime for frs/prod is not deployed. Deploy the environment to bring it up.';

  test('a matching clear marks the warning read; a mismatched one does not', async ({ app }) => {
    const { page } = app;
    await emitRuntimeUnreachable(page, message);

    const warningIcon = app.titlebar.messageCenterIcon('warning');
    await expect(warningIcon).toHaveAccessibleName('Warning: 1 unread');
    await expect(app.titlebar.messageCenterIcon('info')).toHaveCount(0);

    // Sampling over a window catches a "clear everything" once its async SSE clear lands.
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

  // Backed by a real seeded env so opening Manage resolves real config.
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
    // The message text itself contains "Deploy".
    const deployAction = row.getByRole('button', { name: 'Deploy', exact: true });
    await expect(deployAction).toBeVisible();
    await deployAction.click();

    const dialog = new ManageDialog(page, `${SEED_TENANT}-${SEED_ENV_ALPHA}`);
    await dialog.waitForOpen();
    await expect.poll(() => dialog.getActiveTab()).toBe('Runtime');
    await expect(app.titlebar.messageCenterDialog()).toBeHidden();
  });

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

// Polls inside page.evaluate so this stays a bounded in-browser observation, not a spec-side sleep.
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
