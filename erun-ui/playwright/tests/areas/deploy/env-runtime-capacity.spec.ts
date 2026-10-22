import type { Page } from '@playwright/test';

import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';

// stubOversubscribedCluster stages the cluster the reported defect came from: a
// node whose containers' *limits* sum past its allocatable capacity while their
// *requests* leave most of it free -- the normal shape of an erun cluster, since
// the chart sizes requests for scheduling and limits for the work the agent
// runs in the container. The panel read only the first figure, reported "0 CPU
// and 0.0 GiB memory free", predicted a pending deploy from it, and offered
// "lower the request" as the remedy -- the one direction that re-creates the
// out-of-memory kills the runtime limit is sized to avoid.
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
      // holdMs makes this read land a fixed window after the request that
      // asked for it, so a step that is merely slow can be told apart from a
      // step that never converges. Anchored to the request (not to the stub's
      // own registration) so the delay is the same however long the dialog
      // took to open, and only the first read is held -- a refetch is a
      // separate step with its own convergence.
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

    // Each of the three readings below is the answer to the
    // LoadRuntimeResourceStatus round trip this spec's own stub owns, so the
    // step waits for the state the read produces rather than for expect's 10s
    // default -- see the held-read case at the end of this file for the
    // reproduction.
    //
    // The scheduler reading answers the question a deploy follows, and says so.
    await dialog
      .getByText(/the scheduler can admit 13\.5 CPU and 22\.0 GiB/)
      .waitFor({ state: 'visible' });

    // The worst-case reading keeps its answer, labelled as the other question.
    await dialog.getByText(/at its declared limit at once/).waitFor({ state: 'visible' });
    await dialog
      .getByText(/namespace quota or by running fewer environments/)
      .waitFor({ state: 'visible' });

    // The reported behaviour: a pending deploy predicted from the limits sum,
    // with the harmful remedy under it. Neither may appear on this reading.
    await expect(dialog.getByText(/lower the request/)).toHaveCount(0);
    await expect(dialog.getByText(/stays pending until capacity frees up/)).toHaveCount(0);

    // The dialog's default runtime pod is 4 CPU / 8.7 GiB -- the exact values
    // the report records being refused -- and they now submit cleanly.
    await expect(page.locator('#environment-runtime-cpu-value')).toHaveValue('4', withTestBudget());
    await app.envInitDialog.fillEnvironment('review');
    await app.envInitDialog.fillContainerRegistry('ghcr.io/sophium');
    await expect(app.envInitDialog.submitReason()).toHaveText('', withTestBudget());
    await expect(app.envInitDialog.createButton()).toBeEnabled(withTestBudget());

    await app.envInitDialog.cancel();
    await app.envInitDialog.waitForClosed();
  });

  // The panel's content is the answer to LoadRuntimeResourceStatus: the stub
  // above owns it, and an assertion on it carries no timeout of its own --
  // `toContainText`/`toBeVisible` have no waitFor equivalent that outlives
  // expect's default, so the step resolved to a 10s clock the test never
  // declared. Under contention a read that is merely slow therefore reds the
  // test with its own clock unspent, which is how a loaded builder turns a
  // green branch red. The hold below is deliberately just past that 10s
  // default: the smallest delay that discriminates, so the suite pays seconds
  // here rather than the tens a genuinely loaded machine would. The delay is
  // injected at this spec's own stub rather than by loading the machine, so
  // the reproduction is deterministic on a quiet host.
  //
  // Pre-fix this case reds at exactly 10_000ms with 50s of its own budget
  // unused; with the read converged through the test's own budget it passes
  // when the read actually lands.
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
