import { test, expect } from '../fixtures/erunApp.js';
import { SEED_TENANT } from '../fixtures/seedRoot.js';

test.describe('environment init dialog', () => {
  test('opens with tenant pre-populated and cancels', async ({ app }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();
    await expect(app.envInitDialog.locator()).toBeVisible();

    const tenantInput = app.envInitDialog.tenantInput();
    await expect(tenantInput).toBeVisible();
    expect((await tenantInput.inputValue()).trim()).toBe(SEED_TENANT);

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('init mode shows the "create" description and a submit-reason status line', async ({
    app,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // Two copy branches (pre-populated vs blank) both carry "Create", so match loosely.
    const dialog = app.envInitDialog.locator();
    await expect(dialog.getByText(/Create|create/).first()).toBeVisible();

    // The live region stays mounted even with no blockers, so a blocking reason can
    // appear without a layout shift.
    const reason = app.page.locator('#environment-dialog-submit-reason');
    await expect(reason).toHaveCount(1);

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('"Create tenant DevOps repository" checkbox is gone — value is derived from tenant state', async ({
    app,
    page,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    await expect(page.locator('#environment-bootstrap')).toHaveCount(0);
    await expect(page.locator('#environment-default-tenant')).toBeVisible();
    await expect(page.locator('#environment-no-git')).toBeVisible();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('local-agent type reveals the Local repo path field and hides the no-Git toggle', async ({
    app,
    page,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const typeSelect = page.getByLabel('Environment type', { exact: true });
    const localRepoPathInput = page.locator('#environment-local-repo-path');
    const browseButton = page.getByRole('button', { name: /Browse/ });
    const noGitCheckbox = page.locator('#environment-no-git');

    await expect(typeSelect).toBeVisible();
    await expect(localRepoPathInput).toHaveCount(0);
    await expect(browseButton).toHaveCount(0);
    await expect(noGitCheckbox).toBeVisible();

    // The no-Git toggle is hidden for local-agent because it does not affect that
    // init path (see EnvironmentCreateChecks).
    await typeSelect.click();
    await page.getByRole('option', { name: 'Local agent' }).click();
    await expect(localRepoPathInput).toBeVisible();
    await expect(browseButton).toBeVisible();
    await expect(noGitCheckbox).toHaveCount(0);

    await typeSelect.click();
    await page.getByRole('option', { name: 'Runtime' }).click();
    await expect(localRepoPathInput).toHaveCount(0);
    await expect(browseButton).toHaveCount(0);
    await expect(noGitCheckbox).toBeVisible();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('kube-context dropdown survives an environments-changed Wails event', async ({
    app,
    page,
  }) => {
    // Regression guard: an environments-changed tick used to wipe the kube-context
    // dropdown because the Go uiState never carried that field. Firing the event
    // while the dialog is open must leave the same surface visible. The harness's
    // kubectl stub reports no contexts, so that surface is the empty state, never a
    // populated select.
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const select = page.locator('#environment-kubernetes-context');
    const emptyState = app.envInitDialog
      .locator()
      .getByText('No Kubernetes contexts found')
      .first();

    await expect(emptyState).toBeVisible();
    await expect(select).toBeHidden();

    await page.evaluate(() => {
      const runtime = (
        window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
      ).runtime;
      runtime.EventsEmit('environments-changed');
    });

    await expect(emptyState).toBeVisible();
    await expect(select).toBeHidden();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });
});
