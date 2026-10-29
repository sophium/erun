import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';

// Limits sum past allocatable while requests leave most of it free: the normal
// shape of an erun cluster, which the panel must not report as full.
async function stubOversubscribedCluster(
  page: Page,
  options: { holdMs?: number } = {},
): Promise<void> {
  let holdUntil = 0;
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadKubernetesContexts') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: ['erun-node1'] }),
      });
    }
    if (body.method === 'LoadRuntimeResourceStatus') {
      // Anchored to the request so the delay is independent of dialog open time; only the first read is held.
      if (options.holdMs && holdUntil === 0) {
        holdUntil = Date.now() + options.holdMs;
      }
      const remaining = holdUntil - Date.now();
      if (remaining > 0) {
        await new Promise((resolve) => setTimeout(resolve, remaining));
      }
      const metric = (
        total: number,
        used: number,
        free: number,
        unit: string,
        formatted: string,
        floored: boolean,
      ): Record<string, unknown> => ({ total, used, free, unit, formatted, floored });
      const schedulable = {
        cpu: metric(16, 2.5, 13.5, 'cores', '13.5', false),
        memory: metric(32, 10, 22, 'GiB', '22.0 GiB', false),
        message:
          'Right now on erun-node1 (the emptiest node): the scheduler can admit 13.5 CPU and 22.0 GiB memory more. ' +
          'It places by what each pod requests, which is what a deploy depends on -- not by limits, which reserve none of it.',
      };
      const worstCase = {
        cpu: metric(16, 16, 0, 'cores', '0', true),
        memory: metric(32, 32, 0, 'GiB', '0.0 GiB', true),
        message:
          'Worst case, with every container on erun-node1 at its declared limit at once: 0 CPU and 0.0 GiB memory left. ' +
          'That is oversubscription headroom, not scheduling capacity.',
        notice:
          'Oversubscription headroom is managed with a namespace quota or by running fewer environments on this node. ' +
          'Shrinking a limit sized for the work is not the move: it re-creates the out-of-memory kills that destroy an in-pod agent run and its unpushed work.',
      };
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            kubernetesContext: 'erun-node1',
            available: true,
            node: 'erun-node1',
            floored: true,
            measuredUsage: true,
            schedulable,
            schedulableComplete: true,
            worstCase,
            unmeasuredContainers: 2,
            nodes: [{ name: 'erun-node1', schedulable, schedulableComplete: true, worstCase }],
          },
        }),
      });
    }
    await route.continue();
  });
}

test.describe('runtime resources capacity reading', () => {
  test('a limits-oversubscribed node reads as scheduling capacity, and refuses nothing', async ({
    app,
    page,
  }) => {
    await stubOversubscribedCluster(page);
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();
    await app.envInitDialog.selectKubernetesContext('erun-node1');

    const dialog = app.envInitDialog.locator();

    // Each reading answers this spec's own stubbed round trip, so wait for its state rather than expect's 10s default.
    await dialog
      .getByText(/the scheduler can admit 13\.5 CPU and 22\.0 GiB/)
      .waitFor({ state: 'visible' });

    await dialog.getByText(/at its declared limit at once/).waitFor({ state: 'visible' });
    await dialog
      .getByText(/namespace quota or by running fewer environments/)
      .waitFor({ state: 'visible' });

    await expect(dialog.getByText(/lower the request/)).toHaveCount(0);
    await expect(dialog.getByText(/stays pending until capacity frees up/)).toHaveCount(0);

    await expect(page.locator('#environment-runtime-cpu-value')).toHaveValue('4', withTestBudget());
    await app.envInitDialog.fillEnvironment('review');
    await app.envInitDialog.fillContainerRegistry('ghcr.io/sophium');
    await expect(app.envInitDialog.submitReason()).toHaveText('', withTestBudget());
    await expect(app.envInitDialog.createButton()).toBeEnabled(withTestBudget());

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  // The hold is just past expect's 10s default: a slow read must be waited out
  // through the test's own budget, not cut off by an undeclared step clock.
  test('a capacity reading that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    await stubOversubscribedCluster(page, { holdMs: 12_000 });
    await app.sidebar.openInitDialog();
    await app.envInitDialog.waitForOpen();
    await app.envInitDialog.selectKubernetesContext('erun-node1');

    const dialog = app.envInitDialog.locator();
    await expect(dialog.getByText(/the scheduler can admit 13\.5 CPU and 22\.0 GiB/)).toBeVisible(
      withTestBudget(),
    );

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });
});
