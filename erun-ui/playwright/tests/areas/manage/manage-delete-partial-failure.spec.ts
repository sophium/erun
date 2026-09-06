import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';

// A delete whose namespace cleanup fails must render as an error with the
// failure clause visible and a Copy action — not the plain success pill it
// used to be (the copy buffer had already been cleared, and the warning was
// just appended to the "Deleted ..." text as an afterthought).
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
    await expect(pill).toBeVisible();
    await expect(pill).toContainText(`Deleted ${seededEnv.tenant} / ${seededEnv.environment}.`);
    await expect(page.getByRole('button', { name: 'Copy output' })).toBeVisible();
  });
});
