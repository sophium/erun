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
    // No save — the seeded baseline stays untouched. The isolated harness's
    // kubectl stub always answers "no cluster in the Playwright harness" (see
    // fixtures/seedRoot.ts), which is a real probe failure, not a genuine
    // NotFound — so the fixed checkRuntimeDeployed always reports "could not
    // check" here, never a false "not deployed" or a false all-clear.
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    const dialog = app.manageDialog.locator();

    // (a) Effective container registry is shown legibly, not a blank field.
    await expect(app.manageDialog.registryInput(0)).toHaveValue('registry.example/test');

    // (b) The health section is present, and the check is an explicit user action
    // (never implicit on open — Nielsen user control).
    await expect(dialog.getByText('Environment health', { exact: true })).toBeVisible();
    const checkButton = dialog.getByRole('button', { name: 'Check environment' });
    await expect(checkButton).toBeVisible();

    await checkButton.click();

    // After the out-of-pod round-trip a result renders (visibility of system
    // status): the harness cluster is unreachable, so the check reports it
    // could not run — and, critically, offers no Deploy button that would
    // fail identically.
    await expect(dialog.getByText(/Could not check the runtime deployment/)).toBeVisible({
      timeout: 20_000,
    });
    await expect(dialog.getByRole('button', { name: 'Deploy' })).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // A probe failure (VPN down, rotated token, unreachable API server) must
  // report "could not check", never a false "not deployed" that hands the
  // operator a Deploy button that would fail identically. This mirrors the
  // shape checkRuntimeDeployed now returns for a real kubectl error
  // (erun-ui/environment_health_test.go covers the Go side; this spec locks
  // the frontend contract that an "unknown" check renders no Deploy fix).
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

  // A health-check round-trip that fails outright (the Wails call itself
  // rejects, not a computed "unknown" result) renders inline in the dialog —
  // the same surface a save failure uses (Nielsen #4, consistency with
  // comparable flows) — since the modal makes the titlebar pill behind it
  // aria-hidden and therefore inaccessible while open regardless. The
  // catch itself now also routes through showTerminalError rather than
  // showTerminalMessage for the surface that *is* reachable once the dialog
  // closes; close-environment-failure.spec.ts covers a non-modal flow where
  // that distinction is directly observable.
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
