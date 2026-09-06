import type { Request, Route } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';

// erun-kit's StatusBadge is the one canonical status badge in the product.
// ManageDialog.fields.tsx used to define a second, same-named component that
// hand-branched 'running'/'stopped' into raw
// Tailwind colors and rendered no icon at all, so any other status --
// including an in-progress one like 'starting' -- fell into its destructive
// catch-all. Both sites below now render through the canonical component;
// the always-rendered icon is the visible proof, since a hand-rolled
// StatusBadge with the exact same text and no icon would otherwise pass a
// text-only assertion.

async function mutateInvokeResponse(
  page: import('@playwright/test').Page,
  method: string,
  mutate: (data: Record<string, unknown>) => void,
): Promise<void> {
  await page.route('**/__erun_invoke', async (route: Route, request: Request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method !== method) {
      await route.continue();
      return;
    }
    const response = await route.fetch();
    const json = (await response.json()) as { data?: Record<string, unknown> };
    if (json.data) mutate(json.data);
    await route.fulfill({ response, json });
  });
}

// Applies the app's own class-based light/dark mechanism directly (root
// AGENTS.md's Design-Language Decision Record: one shared `.dark` class
// mechanism) rather than clicking the titlebar toggle, which sits behind the
// manage dialog's own overlay while it is open.
async function forceDarkTheme(page: import('@playwright/test').Page): Promise<void> {
  await page.evaluate(() => {
    document.documentElement.classList.add('dark');
  });
}

test('a workspace sync in progress reads as in-progress, with an icon, through the canonical StatusBadge', async ({
  app,
  page,
  seededEnv,
}) => {
  await mutateInvokeResponse(page, 'LoadEnvironmentConfig', (data) => {
    const sshd = data.sshd as Record<string, unknown>;
    sshd.enabled = true;
    sshd.workspaceSyncEnabled = true;
    sshd.workspaceSyncStatus = 'starting';
    sshd.workspaceSyncStatusMessage = 'Starting workspace sync';
  });

  await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
  await app.manageDialog.waitForOpen();
  await app.manageDialog.selectTab('Access');

  const dialog = app.manageDialog.locator();
  const badgeLabel = dialog.getByText('starting', { exact: true });
  await expect(badgeLabel).toBeVisible();
  await expect(badgeLabel.locator('xpath=..').locator('svg')).toHaveCount(1);
  await badgeLabel.scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'test-results/status-badge-workspace-sync-light.png' });

  await forceDarkTheme(page);
  await page.screenshot({ path: 'test-results/status-badge-workspace-sync-dark.png' });

  await app.manageDialog.cancel();
  await app.manageDialog.waitForClosed();
});

test('a pending cloud context reads as in-progress, with an icon, through the canonical StatusBadge', async ({
  app,
  page,
  seededEnv,
}) => {
  await mutateInvokeResponse(page, 'LoadEnvironmentConfig', (data) => {
    data.cloudContext = {
      name: 'pw-aws',
      provider: 'aws',
      cloudProviderAlias: 'pw-aws',
      region: 'us-east-1',
      instanceType: 't3.medium',
      diskType: 'gp3',
      diskSizeGb: 40,
      kubernetesContext: 'pw-aws-context',
      status: 'pending',
    };
  });

  await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
  await app.manageDialog.waitForOpen();
  await app.manageDialog.selectTab('General');

  const dialog = app.manageDialog.locator();
  const badgeLabel = dialog.getByText('Pending', { exact: true });
  await expect(badgeLabel).toBeVisible();
  await expect(badgeLabel.locator('xpath=..').locator('svg')).toHaveCount(1);
  await badgeLabel.scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'test-results/status-badge-cloud-context-light.png' });

  await forceDarkTheme(page);
  await page.screenshot({ path: 'test-results/status-badge-cloud-context-dark.png' });

  await app.manageDialog.cancel();
  await app.manageDialog.waitForClosed();
});
