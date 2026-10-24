import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

async function stubCheckEnvironmentHealth(
  page: Page,
  body: Record<string, unknown>,
): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const parsed = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (parsed.method === 'CheckEnvironmentHealth') {
      return route.fulfill({ contentType: 'application/json', body: JSON.stringify(body) });
    }
    await route.continue();
  });
}

test.describe('manage dialog environment health check', () => {
  test('shows the effective registry and runs an explicit health check reporting the harness cluster as unreachable', async ({
    app,
  }) => {
    // No save. The harness kubectl stub always fails to reach a cluster, so the
    // check reports "could not check", never "not deployed".
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    const dialog = app.manageDialog.locator();

    await expect(app.manageDialog.registryInput(0)).toHaveValue('registry.example/test');

    // The check is an explicit user action, never implicit on open.
    await expect(dialog.getByText('Environment health', { exact: true })).toBeVisible();
    const checkButton = dialog.getByRole('button', { name: 'Check environment' });
    await expect(checkButton).toBeVisible();

    await checkButton.click();

    await expect(dialog.getByText(/Could not check the runtime deployment/)).toBeVisible({
      timeout: 20_000,
    });
    await expect(dialog.getByRole('button', { name: 'Deploy' })).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('a probe that could not run reports "could not check" and offers no Deploy button (#1212)', async ({
    app,
    page,
  }) => {
    await stubCheckEnvironmentHealth(page, {
      data: {
        tenant: SEED_TENANT,
        environment: SEED_ENV_ALPHA,
        healthy: false,
        checks: [
          {
            id: 'registry',
            status: 'ok',
            title: 'Container registry',
            detail: 'Using registry.example/test.',
          },
          {
            id: 'runtime-deployed',
            status: 'unknown',
            title: 'Runtime deployment',
            detail:
              'Could not check the runtime deployment on orbstack: Unable to connect to the server: dial tcp: i/o timeout',
          },
        ],
      },
    });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    const dialog = app.manageDialog.locator();

    await dialog.getByRole('button', { name: 'Check environment' }).click();

    await expect(dialog.getByText(/Could not check the runtime deployment/)).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Deploy' })).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The modal makes the titlebar pill aria-hidden, so the failure must render inline.
  test('a health-check RPC failure surfaces inline in the dialog, not silently', async ({
    app,
    page,
  }) => {
    await stubCheckEnvironmentHealth(page, { error: 'HEALTH_CHECK_RPC_UNREACHABLE_MARKER' });
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    const dialog = app.manageDialog.locator();

    await dialog.getByRole('button', { name: 'Check environment' }).click();

    await expect(
      dialog.getByRole('alert').filter({ hasText: 'HEALTH_CHECK_RPC_UNREACHABLE_MARKER' }),
    ).toBeVisible();

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
