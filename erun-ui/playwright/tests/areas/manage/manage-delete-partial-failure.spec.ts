import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';

async function stubDeleteWithNamespaceFailure(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string; args?: unknown[] };
    if (body.method === 'DeleteEnvironment') {
      const selection = (body.args?.[0] ?? {}) as { tenant?: string; environment?: string };
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            tenant: selection.tenant,
            environment: selection.environment,
            namespaceDeleteError: 'NAMESPACE_DELETE_UNREACHABLE_MARKER',
          },
        }),
      });
    }
    await route.continue();
  });
}

test.describe('manage dialog delete — partial failure (#1212)', () => {
  test('a namespace cleanup failure renders as an error with a Copy action, not a silent success pill', async ({
    app,
    page,
    seededEnv,
  }) => {
    await stubDeleteWithNamespaceFailure(page);
    await app.sidebar.openManageDialogViaKeyboard(seededEnv.tenant, seededEnv.environment);
    await app.manageDialog.waitForOpen();

    await app.manageDialog.openDelete();
    await app.manageDialog.confirmDelete(`${seededEnv.tenant}-${seededEnv.environment}`);

    const pill = page.getByRole('alert').filter({ hasText: 'NAMESPACE_DELETE_UNREACHABLE_MARKER' });
    // The delete round trip and the result-pill swap are separate steps.
    await pill.waitFor({ state: 'visible' });
    await expect(pill).toBeVisible();
    await expect(pill).toContainText(`Deleted ${seededEnv.tenant} / ${seededEnv.environment}.`);
    await expect(page.getByRole('button', { name: 'Copy output' })).toBeVisible();
  });
});
