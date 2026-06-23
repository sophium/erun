import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// Issue #641 — a disabled or at-risk Save must explain itself where the user
// acts (NN #1 visibility of system status, #9 recovery). When the env's runtime
// request fits no single node, that is a deploy-time capacity concern, not a
// config-validity one: Save stays enabled (it only persists config) and the
// footer shows a non-blocking warning that points to the Runtime tab where the
// request is lowered. Runtime resource status is stubbed over /__erun_invoke
// (the manage-redeploy-banner technique) to a split-node shape that no single
// node can satisfy — the harness has no cluster, so this state is otherwise
// unreachable.
const metric = (free: number, total: number) => ({
  total,
  used: total - free,
  free,
  unit: 'GiB',
  formatted: String(free),
});

const noSingleNodeFitsStatus = {
  kubernetesContext: 'test-context',
  available: true,
  message: 'Available on best node: 8 CPU, 16 GiB memory.',
  cpu: metric(8, 16),
  memory: metric(16, 32),
  // Aggregate capacity looks ample, but each node is lopsided: node-a has CPU
  // but almost no memory, node-b the reverse — so the ~4 CPU / 8.7 GiB default
  // request fits neither.
  nodes: [
    { name: 'node-a', cpu: metric(8, 8), memory: metric(1, 16) },
    { name: 'node-b', cpu: metric(1, 8), memory: metric(16, 16) },
  ],
};

test.describe('manage dialog runtime capacity feedback (#641)', () => {
  test.beforeEach(async ({ page }) => {
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadRuntimeResourceStatus') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: noSingleNodeFitsStatus }),
        });
      }
      await route.continue();
    });
  });

  test('an unschedulable request warns without blocking Save, and points to Runtime', async ({
    app,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    // Capacity is a deploy-time concern, not config validity — Save stays usable
    // so the operator is never trapped editing an unrelated field.
    await expect(app.manageDialog.saveButton()).toBeEnabled();

    // The reason is shown where the user acts, not buried on the Runtime tab.
    await expect(app.manageDialog.saveStatus()).toContainText('No node currently has');

    // The offending tab is marked (icon + accessible label) so the user knows
    // where to fix it.
    await expect.poll(() => app.manageDialog.tabHasWarning('Runtime')).toBe(true);

    // The recovery action navigates straight to the Runtime tab.
    await app.manageDialog.goToRuntimeButton().click();
    await expect.poll(() => app.manageDialog.getActiveTab()).toBe('Runtime');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
