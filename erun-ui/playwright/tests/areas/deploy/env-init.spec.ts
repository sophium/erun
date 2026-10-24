import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Fakes a reachable cluster so value requirements are the only gate blockers.
// One route handler, not stacked ones: route.continue() goes to the real backend
// rather than an earlier handler, so stacking defeats the first stub.
// LoadHostedRegistry makes a real outbound call page.route can't intercept, so the
// harness default (unavailable) comes from ERUN_HOSTED_REGISTRY_PROBE_OVERRIDE=0.
async function stubDialogCluster(
  page: Page,
  clusterRegistryFailure?: string,
  hostedRegistryAvailable?: boolean,
  holdResourceStatusMs?: number,
): Promise<void> {
  let holdUntil = 0;
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadKubernetesContexts') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: ['orbstack'] }),
      });
    }
    if (body.method === 'LoadRuntimeResourceStatus') {
      // Held on the first read only, so slow can be told apart from never-converging.
      if (holdResourceStatusMs && holdUntil === 0) {
        holdUntil = Date.now() + holdResourceStatusMs;
      }
      const remaining = holdUntil - Date.now();
      if (remaining > 0) {
        await new Promise((resolve) => setTimeout(resolve, remaining));
      }
      // Mirrors the backend contract: a node name plus scheduler and worst-case readings.
      const schedulable = {
        cpu: { total: 8, used: 0, free: 8, unit: 'cores', formatted: '8', floored: false },
        memory: {
          total: 16,
          used: 0,
          free: 16,
          unit: 'GiB',
          formatted: '16.0 GiB',
          floored: false,
        },
        message:
          'Right now on node-a (the emptiest node): the scheduler can admit 8 CPU and 16.0 GiB memory more.',
      };
      const worstCase = {
        cpu: { total: 8, used: 2, free: 6, unit: 'cores', formatted: '6', floored: false },
        memory: {
          total: 16,
          used: 4,
          free: 12,
          unit: 'GiB',
          formatted: '12.0 GiB',
          floored: false,
        },
        message:
          'Worst case, with every container on node-a at its declared limit at once: 6 CPU and 12.0 GiB memory left.',
      };
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            kubernetesContext: 'orbstack',
            available: true,
            node: 'node-a',
            floored: false,
            measuredUsage: true,
            schedulable,
            schedulableComplete: true,
            worstCase,
            nodes: [{ name: 'node-a', schedulable, schedulableComplete: true, worstCase }],
          },
        }),
      });
    }
    if (body.method === 'LoadClusterRegistry' && clusterRegistryFailure) {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ error: clusterRegistryFailure }),
      });
    }
    if (body.method === 'LoadHostedRegistry' && hostedRegistryAvailable) {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: { host: 'registry.erunpaas.com', available: true } }),
      });
    }
    await route.continue();
  });
}

// The probe fails (VPN, RBAC), which must not read as "no registry found".
async function stubDialogClusterWithRegistryFailure(page: Page): Promise<void> {
  await stubDialogCluster(page, 'CLUSTER_REGISTRY_PROBE_UNREACHABLE_MARKER');
}

async function stubDialogClusterWithHostedRegistryAvailable(page: Page): Promise<void> {
  await stubDialogCluster(page, undefined, true);
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
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // getByRole reads the accessible name ("(required)" in, aria-hidden "*" out);
    // getByLabel would include the glyph.
    for (const name of [
      'Tenant (required)',
      'Environment (required)',
      'Environment type (required)',
      'Kubernetes context (required)',
      'Container registry (required)',
    ]) {
      await expect(page.getByRole('combobox', { name, exact: true })).toBeVisible();
    }

    await expect(page.locator('label[for="environment-container-registry"]')).toContainText('*');

    await expect(page.getByLabel('Runtime version (required)', { exact: true })).toHaveCount(0);

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('init mode shows the "create" description and a submit-reason status line', async ({
    app,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // Both copy branches contain "Create", so match loosely.
    const dialog = app.envInitDialog.locator();
    await expect(dialog.getByText(/Create|create/).first()).toBeVisible();

    // Stays mounted with no blockers so a reason appears without a layout shift.
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

    // By role+name: getByLabel would include the aria-hidden "*".
    const typeSelect = page.getByRole('combobox', { name: 'Environment type (required)' });
    const localRepoPathInput = page.locator('#environment-local-repo-path');
    const browseButton = page.getByRole('button', { name: /Browse/ });
    const noGitCheckbox = page.locator('#environment-no-git');

    await expect(typeSelect).toBeVisible();
    await expect(localRepoPathInput).toHaveCount(0);
    await expect(browseButton).toHaveCount(0);
    await expect(noGitCheckbox).toBeVisible();

    // The no-Git toggle doesn't affect local-agent init (see EnvironmentCreateChecks).
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
    // The kubectl stub reports no contexts, so the surface is the empty state.
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
    // The WebView shows no native validity bubbles, so the gate names the first missing value.
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const createButton = app.envInitDialog.createButton();
    const reason = app.envInitDialog.submitReason();

    // The context is listed but not preselected. Reads wait on the test's budget.
    await expect(app.envInitDialog.kubernetesContextTrigger()).toContainText(
      'Select Kubernetes context',
      withTestBudget(),
    );
    await expect(app.envInitDialog.locator().getByText('No Kubernetes contexts found')).toHaveCount(
      0,
    );

    await expect(createButton).toBeDisabled();
    await expect(reason).toHaveText('Enter a tenant and environment name.');

    await app.envInitDialog.fillEnvironment('review');
    await expect(createButton).toBeDisabled();
    await expect(reason).toHaveText('Select a Kubernetes context.', withTestBudget());

    await app.envInitDialog.selectKubernetesContext('orbstack');
    await expect(createButton).toBeDisabled();
    await expect(reason).toHaveText('Select a container registry.', withTestBudget());

    await app.envInitDialog.fillContainerRegistry('ghcr.io/sophium');
    await expect(createButton).toBeEnabled(withTestBudget());
    await expect(reason).toHaveText('', withTestBudget());

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('runtime capacity is shown only after a Kubernetes context is selected', async ({
    app,
    page,
  }) => {
    // Capacity appears only for an explicitly selected context.
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const capacity = app.envInitDialog.locator().getByText(/Right now on /);

    await expect(app.envInitDialog.kubernetesContextTrigger()).toContainText(
      'Select Kubernetes context',
      withTestBudget(),
    );
    await expect(capacity).toHaveCount(0);

    await app.envInitDialog.selectKubernetesContext('orbstack');
    await capacity.waitFor({ state: 'visible' });

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('does not pre-check default-tenant and offers a skip-Git-checkout option', async ({
    app,
    page,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // Off by default: creating an env must not repoint the default tenant.
    await expect(page.locator('#environment-default-tenant')).not.toBeChecked();

    const skipGit = page.locator('#environment-no-git');
    await expect(skipGit).toBeVisible();
    await expect(skipGit).not.toBeChecked();
    await skipGit.click();
    await expect(skipGit).toBeChecked();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  // The popover needs suggestions, so notices render outside it.
  test('shows the version recovery advice even when the popover has nothing to list', async ({
    app,
    page,
  }) => {
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadVersionSuggestions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: {
              suggestions: [],
              notices: [{ image: 'ghcr.io/acme/erun-devops', kind: 'auth' }],
            },
          }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    await expect(app.envInitDialog.versionChoicesButton()).toBeDisabled(withTestBudget());
    const notices = app.envInitDialog.versionNotices();
    await notices.waitFor({ state: 'visible' });
    await expect(notices).toContainText('ghcr.io/acme/erun-devops is private', withTestBudget());
    await expect(notices).toContainText('docker login ghcr.io', withTestBudget());

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('names the managed-cloud-cluster route when no Kubernetes contexts are found', async ({
    app,
  }) => {
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const emptyState = app.envInitDialog.locator().getByText('No Kubernetes contexts found');
    await expect(emptyState).toBeVisible();
    const body = app.envInitDialog.locator();
    await expect(body).toContainText('Cloud aliases');
    await expect(body).toContainText('Cloud contexts');

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  // The documented Windows getting-started path; copy must not assume macOS.
  test.describe('on a Windows user agent', () => {
    test.use({
      userAgent:
        'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36',
    });

    test('names Windows recovery steps instead of macOS-only ones', async ({ app }) => {
      await app.sidebar.openInitDialog();
      await app.envInitDialog.waitForOpen();

      const body = app.envInitDialog.locator();
      await expect(body).toContainText('winget install');
      await expect(body).not.toContainText('brew install');
      await expect(body).not.toContainText('.zshenv');

      await app.envInitDialog.cancel();
      await app.envInitDialog.waitForClosed();
    });
  });

  test('explains the container registry format and the auto-detect route when nothing is detected', async ({
    app,
    page,
  }) => {
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // The kubectl stub can't reach a cluster, so no registry is detected.
    await app.envInitDialog.selectKubernetesContext('orbstack');
    await expect(page.getByText('Use in-cluster registry', { exact: false })).toHaveCount(0);

    const help = app.envInitDialog.locator().getByText(/Where images push to and pull from/);
    await expect(help).toBeVisible();
    await expect(help).toContainText('ghcr.io/your-org');
    await expect(help).toContainText('Cloud aliases');

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('a cluster-registry probe failure shows the failure, not a silent reset (#1212)', async ({
    app,
    page,
  }) => {
    await stubDialogClusterWithRegistryFailure(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    await app.envInitDialog.selectKubernetesContext('orbstack');

    await expect(app.envInitDialog.locator().getByRole('alert')).toContainText(
      'CLUSTER_REGISTRY_PROBE_UNREACHABLE_MARKER',
      withTestBudget(),
    );

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  // LoadRuntimeResourceStatus is held just past expect's 10s default; the reading must
  // still be waited out on the test's own budget.
  test('a capacity reading that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    await stubDialogCluster(page, undefined, undefined, 12_000);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const capacity = app.envInitDialog.locator().getByText(/Right now on /);
    await app.envInitDialog.selectKubernetesContext('orbstack');
    await capacity.waitFor({ state: 'visible' });
    await expect(capacity).toContainText('Right now on node-a', withTestBudget());

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('offers the hosted registry only once it is confirmed reachable', async ({ app, page }) => {
    // The harness default is "unavailable", so the checkbox stays disabled and explains why.
    await stubDialogCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    // The hosted-registry probe is a real outbound call, the slowest read here.
    const hosted = app.envInitDialog.hostedRegistryCheckbox();
    await hosted.waitFor({ state: 'visible' });
    await expect(hosted).toBeDisabled(withTestBudget());
    await expect(app.envInitDialog.locator()).toContainText('does not resolve', withTestBudget());

    await app.envInitDialog.selectKubernetesContext('orbstack');
    await app.envInitDialog.fillEnvironment('review');
    await app.envInitDialog.fillContainerRegistry('ghcr.io/sophium');
    await expect(app.envInitDialog.createButton()).toBeEnabled(withTestBudget());

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  test('once reachable, selecting the hosted registry clears the container-registry requirement', async ({
    app,
    page,
  }) => {
    await stubDialogClusterWithHostedRegistryAvailable(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();

    const hosted = app.envInitDialog.hostedRegistryCheckbox();
    await expect(hosted).toBeEnabled(withTestBudget());

    await app.envInitDialog.selectKubernetesContext('orbstack');
    await app.envInitDialog.fillEnvironment('review');
    // The hosted registry needs no container registry value.
    await expect(app.envInitDialog.createButton()).toBeDisabled(withTestBudget());

    await hosted.click();
    await expect(hosted).toBeChecked(withTestBudget());
    await expect(app.envInitDialog.createButton()).toBeEnabled(withTestBudget());
    await expect(app.envInitDialog.submitReason()).toHaveText('', withTestBudget());

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });
});
