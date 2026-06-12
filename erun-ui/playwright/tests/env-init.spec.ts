import { test, expect } from '../fixtures/erunApp.js';
import { SEED_TENANT } from '../fixtures/seedRoot.js';

test.describe('environment init dialog', () => {
  test('opens with tenant pre-populated and cancels', async ({ app }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();
    await expect(app.envInitDialog.locator()).toBeVisible();

    // The dialog pre-populates the tenant field with the current
    // selection's tenant — the seeded baseline tenant.
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

    // Default is remote-agent — the no-Git toggle is visible and
    // LocalRepoPath / Browse are absent from the DOM.
    await expect(typeSelect).toBeVisible();
    await expect(localRepoPathInput).toHaveCount(0);
    await expect(browseButton).toHaveCount(0);
    await expect(noGitCheckbox).toBeVisible();

    // Switch to local-agent: LocalRepoPath + Browse appear, the no-Git
    // toggle drops out (it doesn't influence the local-agent init path
    // — see EnvironmentCreateChecks for the rationale).
    await typeSelect.click();
    await page.getByRole('option', { name: 'Local agent' }).click();
    await expect(localRepoPathInput).toBeVisible();
    await expect(browseButton).toBeVisible();
    await expect(noGitCheckbox).toHaveCount(0);

    // Switch to runtime: LocalRepoPath disappears, no-Git returns.
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
    // Regression: reloadStateAfterEnvironmentChange used to dispatch
    // selectLoadedKubernetesContexts(loaded.kubernetesContexts ?? []),
    // but the Go uiState never carried that field — every
    // environments-changed tick overwrote the populated dropdown with
    // []. Invariant: while the dialog is open, firing the event must
    // leave the same surface visible. The isolated harness's kubectl stub
    // reports no contexts, so the deterministic surface is the empty state
    // (rendered as a status block with a Rescan action), and the populated
    // select must never appear.
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const select = page.locator('#environment-kubernetes-context');
    const emptyState = app.envInitDialog
      .locator()
      .getByText('No Kubernetes contexts found')
      .first();

    // Wait for "Loading contexts..." to clear into the empty state.
    await expect(emptyState).toBeVisible();
    await expect(select).toBeHidden();

    await page.evaluate(() => {
      const runtime = (
        window as unknown as { runtime: { EventsEmit: (n: string, ...a: unknown[]) => void } }
      ).runtime;
      runtime.EventsEmit('environments-changed');
    });

    // Allow the dispatch chain (event → reload → store update) to
    // settle, then assert the surface we started with is still visible.
    await expect(emptyState).toBeVisible();
    await expect(select).toBeHidden();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });
});
