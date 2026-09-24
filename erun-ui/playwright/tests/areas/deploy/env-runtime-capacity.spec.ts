import type { Page } from '@playwright/test';

import { test, expect } from '../../../fixtures/erunApp.js';

// stubOversubscribedCluster stages the cluster the reported defect came from: a
// node whose containers' *limits* sum past its allocatable capacity while their
// *requests* leave most of it free -- the normal shape of an erun cluster, since
// the chart sizes requests for scheduling and limits for the work the agent
// runs in the container. The panel read only the first figure, reported "0 CPU
// and 0.0 GiB memory free", predicted a pending deploy from it, and offered
// "lower the request" as the remedy -- the one direction that re-creates the
// out-of-memory kills the runtime limit is sized to avoid.
async function stubOversubscribedCluster(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method: string };
    if (body.method === 'LoadKubernetesContexts') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: ['erun-node1'] }),
      });
    }
    if (body.method === 'LoadRuntimeResourceStatus') {
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

    // The scheduler reading answers the question a deploy follows, and says so.
    await expect(dialog.getByText(/the scheduler can admit 13\.5 CPU and 22\.0 GiB/)).toBeVisible();

    // The worst-case reading keeps its answer, labelled as the other question.
    await expect(dialog.getByText(/at its declared limit at once/)).toBeVisible();
    await expect(dialog.getByText(/namespace quota or by running fewer environments/)).toBeVisible();

    // The reported behaviour: a pending deploy predicted from the limits sum,
    // with the harmful remedy under it. Neither may appear on this reading.
    await expect(dialog.getByText(/lower the request/)).toHaveCount(0);
    await expect(dialog.getByText(/stays pending until capacity frees up/)).toHaveCount(0);

    // The dialog's default runtime pod is 4 CPU / 8.7 GiB -- the exact values
    // the report records being refused -- and they now submit cleanly.
    await expect(page.locator('#environment-runtime-cpu-value')).toHaveValue('4');
    await app.envInitDialog.fillEnvironment('review');
    await app.envInitDialog.fillContainerRegistry('ghcr.io/sophium');
    await expect(app.envInitDialog.submitReason()).toHaveText('');
    await expect(app.envInitDialog.createButton()).toBeEnabled();

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });
});
