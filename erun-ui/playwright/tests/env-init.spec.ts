import { test, expect } from '../fixtures/erunApp.js';

test.describe('environment init dialog', () => {
  test('opens with tenant pre-populated and cancels', async ({ app }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();
    await expect(app.envInitDialog.locator()).toBeVisible();

    // When at least one tenant exists, the dialog pre-populates the
    // tenant field with the current selection's tenant; assert the field
    // is present and non-empty.
    const tenantInput = app.envInitDialog.tenantInput();
    await expect(tenantInput).toBeVisible();
    const tenant = (await tenantInput.inputValue()).trim();
    expect(tenant.length).toBeGreaterThan(0);

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('init mode shows the "create" description and a submit-reason status line', async ({
    app,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // The new mode-aware description text (item 8 in the UX plan) must
    // mention "Create" rather than the generic "Enter the tenant and
    // environment name." copy. Match a regex so both the "with
    // pre-populated values" and "blank" copy branches pass.
    const dialog = app.envInitDialog.locator();
    await expect(dialog.getByText(/Create|create/).first()).toBeVisible();

    // The submit-disabled reason container exists with role=status; it
    // may be empty when the dialog has no current blockers. The
    // important invariant is that the live-region container is in the
    // DOM (item 11) so blocking reasons can surface there without a
    // re-render shift.
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

    // The Bootstrap toggle used to live at #environment-bootstrap. The
    // value is now computed by environmentDialogSelection
    // (!tenantExists), so the checkbox must not be in the DOM under any
    // dialog state.
    await expect(page.locator('#environment-bootstrap')).toHaveCount(0);
    // The "Set as default tenant" and "Initialize without Git checkout"
    // toggles still belong to the dialog — confirm they're untouched.
    await expect(page.locator('#environment-default-tenant')).toBeVisible();
    await expect(page.locator('#environment-no-git')).toBeVisible();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('kube-context dropdown survives an environments-changed Wails event', async ({
    app,
    page,
  }) => {
    // Regression: reloadStateAfterEnvironmentChange used to dispatch
    // selectLoadedKubernetesContexts(loaded.kubernetesContexts ?? []),
    // but the Go uiState never carried that field — every
    // environments-changed tick overwrote the populated dropdown with
    // []. Invariant: while the dialog is open, firing the event must
    // leave the same surface visible (populated stays populated, empty
    // stays empty). The test works whether or not the dev machine has
    // kubectl contexts available.
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const select = page.locator('#environment-kubernetes-context');
    const emptyState = app.envInitDialog
      .locator()
      .getByRole('heading', { name: 'No Kubernetes contexts found' });

    // Wait for "Loading contexts..." to clear: either surface is fine.
    await expect(select.or(emptyState)).toBeVisible();
    const wasPopulated = await select.isVisible().catch(() => false);

    await page.evaluate(() => {
      const runtime = (
        window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
      ).runtime;
      runtime.EventsEmit('environments-changed');
    });

    // Allow the dispatch chain (event → reload → store update) to
    // settle, then assert the surface we started with is still visible.
    const expectedSurface = wasPopulated ? select : emptyState;
    const otherSurface = wasPopulated ? emptyState : select;
    await expect(expectedSurface).toBeVisible();
    await expect(otherSurface).toBeHidden();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });
});
