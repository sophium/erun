import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures/erunApp.js';
import { SEED_TENANT } from '../fixtures/seedRoot.js';

// stubDialogCluster makes the env-init dialog behave as if a real cluster were
// reachable: the kubectl stub in the isolated harness reports no contexts (the
// deterministic empty state), so without this the context blocker masks every
// other gate reason. One context lets the dialog auto-select it, and an
// available resource status clears the capacity blocker, leaving the value
// requirements (environment name, container registry) as the only blockers —
// which is exactly what this spec exercises.
async function stubDialogCluster(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadKubernetesContexts') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: ['orbstack'] }),
      });
    }
    if (body.method === 'LoadRuntimeResourceStatus') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            kubernetesContext: 'orbstack',
            available: true,
            cpu: { total: 8, used: 0, free: 8, unit: 'cores', formatted: '8' },
            memory: { total: 16, used: 0, free: 16, unit: 'GiB', formatted: '16' },
          },
        }),
      });
    }
    await route.continue();
  });
}

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

  test('marks mandatory fields with a required indicator (not colour-only)', async ({
    app,
    page,
  }) => {
    // The reported gap: Create sat disabled with no on-field cue for which values
    // were mandatory — only a one-at-a-time footer reason. Every required field now
    // carries a marker, and the requirement folds into the accessible name via the
    // label's visually-hidden "(required)", so it is conveyed non-visually too.
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // getByRole(name) reads the accessible name — the exact surface this test is
    // about: the "(required)" folds in via the sr-only span, while the visible
    // "*" glyph (aria-hidden) is excluded. getByLabel matches raw label
    // textContent, which includes that glyph ("Tenant* (required)"), so it is the
    // wrong query for an accessible-name assertion.
    for (const name of [
      'Tenant (required)',
      'Environment (required)',
      'Environment type (required)',
      'Kubernetes context (required)',
      'Container registry (required)',
    ]) {
      await expect(page.getByRole('combobox', { name, exact: true })).toBeVisible();
    }

    // The visible marker is a glyph, present in the label DOM.
    await expect(page.locator('label[for="environment-container-registry"]')).toContainText('*');

    // Optional fields (e.g. Runtime version) carry no requirement marker.
    await expect(page.getByLabel('Runtime version (required)', { exact: true })).toHaveCount(0);

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

    // The control's accessible name carries the folded-in "(required)"; match it
    // by role+name rather than getByLabel (which sees the raw label textContent,
    // including the aria-hidden "*").
    const typeSelect = page.getByRole('combobox', { name: 'Environment type (required)' });
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

  test('keeps Create disabled with a visible reason until every required value is provided', async ({
    app,
    page,
  }) => {
    // Regression (the reported bug): the submit gate only checked that the
    // Kubernetes-context *list* was non-empty — never that a context was actually
    // selected or a container registry chosen. A context can be available yet
    // unselected (the reported state), so the button looked active, but clicking
    // hit the invalid-selection branch whose only feedback was a native validity
    // bubble the Wails WebView does not render — Create silently did nothing. The
    // gate now derives from the same required-field rules submit enforces and
    // surfaces the first missing value, walking the fields as the user fills them.
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const createButton = app.envInitDialog.createButton();
    const reason = app.envInitDialog.submitReason();

    // The stub populates one context but the dialog does not preselect it — the
    // trigger sits on its placeholder, reproducing the reported state.
    await expect(app.envInitDialog.locator().getByText('No Kubernetes contexts found')).toHaveCount(
      0,
    );
    await expect(app.envInitDialog.kubernetesContextTrigger()).toContainText(
      'Select Kubernetes context',
    );

    // Empty environment name is the first blocker; Create is disabled and says why.
    await expect(createButton).toBeDisabled();
    await expect(reason).toHaveText('Enter a tenant and environment name.');

    // Name provided, context still unselected: the button stays disabled and now
    // names the missing selection instead of silently doing nothing on click.
    await app.envInitDialog.fillEnvironment('review');
    await expect(createButton).toBeDisabled();
    await expect(reason).toHaveText('Select a Kubernetes context.');

    // Selecting the context advances to the container registry — the required
    // field the old gate never checked, which is what left Create wrongly active.
    await app.envInitDialog.selectKubernetesContext('orbstack');
    await expect(createButton).toBeDisabled();
    await expect(reason).toHaveText('Select a container registry.');

    // Providing the registry clears the last value blocker, so Create activates
    // and the reason line goes quiet.
    await app.envInitDialog.fillContainerRegistry('ghcr.io/sophium');
    await expect(createButton).toBeEnabled();
    await expect(reason).toHaveText('');

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('runtime capacity is shown only after a Kubernetes context is selected', async ({
    app,
    page,
  }) => {
    // Reported bug: the RUNTIME RESOURCES panel showed "Available on best node: N
    // CPU …" while the context dropdown still sat on its "Select Kubernetes
    // context" placeholder — capacity for a cluster the user never chose. The
    // dialog no longer auto-resolves contexts[0] for the capacity fetch, so
    // capacity appears only for an explicitly selected context.
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const capacity = app.envInitDialog.locator().getByText(/Available on best node/);

    // One context is available but not preselected — so no capacity is shown.
    await expect(app.envInitDialog.kubernetesContextTrigger()).toContainText(
      'Select Kubernetes context',
    );
    await expect(capacity).toHaveCount(0);

    // Selecting the context fetches and reveals its capacity.
    await app.envInitDialog.selectKubernetesContext('orbstack');
    await expect(capacity).toBeVisible();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('does not pre-check default-tenant and offers a skip-Git-checkout option', async ({
    app,
    page,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // "Set as default tenant" is OFF by default — creating an environment must not
    // silently repoint the operator's default tenant.
    await expect(page.locator('#environment-default-tenant')).not.toBeChecked();

    // A remote-agent (the default type) offers skipping the Git checkout, off by
    // default and toggleable — the way to create without the remote-worktree flow.
    const skipGit = page.locator('#environment-no-git');
    await expect(skipGit).toBeVisible();
    await expect(skipGit).not.toBeChecked();
    await skipGit.click();
    await expect(skipGit).toBeChecked();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });
});
