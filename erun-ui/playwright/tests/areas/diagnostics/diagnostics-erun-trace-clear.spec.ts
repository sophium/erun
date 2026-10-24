import { expect, test, waitForSeededRow, withTestBudget } from '../../../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../../../fixtures/seedRoot.js';

// Clear is non-destructive: it baselines the view without truncating the log or Copy reads.
test.describe('diagnostics erun-trace clear', () => {
  test.beforeEach(async ({ app }) => {
    if (!(await app.debugPanel.isOpen())) {
      await app.debugPanel.toggle();
      await app.debugPanel.waitForOpen();
    }
    await app.debugPanel.selectTab('erun trace');
  });

  test.afterEach(async ({ app }) => {
    if (await app.debugPanel.isOpen()) {
      await app.debugPanel.toggle();
      // Converge on the close so a slow restore can't leak an open panel into the next test.
      await app.debugPanel.waitForClosed();
    }
  });

  test('Clear baselines the view; new lines stand out and Show all restores', async ({
    app,
    page,
    seededEnv,
  }) => {
    let extra = '';
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'LoadEnvTrace') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: {
              available: true,
              path: `/seed/${seededEnv.tenant}/${seededEnv.environment}/trace.log`,
              content: `TRACE-OLD-1\nTRACE-OLD-2\n${extra}`,
            },
          }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    const pane = app.debugPanel.erunTracePane();
    await expect(pane).toContainText('TRACE-OLD-1', withTestBudget());

    await app.debugPanel.erunTraceClearButton().click();
    const clearedNotice = page.getByText('Showing entries since you cleared.');
    await clearedNotice.waitFor({ state: 'visible' });
    await expect(clearedNotice).toBeVisible();
    await expect(pane).not.toContainText('TRACE-OLD-1');
    await expect(pane).toContainText('No new entries since you cleared.', withTestBudget());

    extra = 'TRACE-NEW-1\n';
    await expect(pane).toContainText('TRACE-NEW-1', withTestBudget());
    await expect(pane).not.toContainText('TRACE-OLD-1');

    // Show all proves Clear hid rather than truncated.
    await app.debugPanel.erunTraceShowAllButton().click();
    await expect(pane).toContainText('TRACE-OLD-1', withTestBudget());
    await clearedNotice.waitFor({ state: 'hidden' });
    await expect(clearedNotice).toBeHidden();
  });

  test('the baseline is per-env: switching envs resets it', async ({ app, page, seededEnv }) => {
    // Throwaway envs so the shared baseline rows are never left open for other specs.
    const envA = seededEnv.environment;
    const envB = uniqueEnvironmentName('per-env-reset-b');
    seedEnvironment(SEED_TENANT, envB);
    try {
      await waitForSeededRow(app, SEED_TENANT, envB);

      await page.route('**/__erun_invoke', async (route, request) => {
        const body = JSON.parse(request.postData() ?? '{}') as {
          method: string;
          args?: { environment?: string }[];
        };
        if (body.method === 'LoadEnvTrace') {
          const envName = body.args?.[0]?.environment ?? '';
          return route.fulfill({
            contentType: 'application/json',
            body: JSON.stringify({
              data: {
                available: true,
                path: `/seed/trace.log`,
                content: `LINE-FOR-${envName}-1\nLINE-FOR-${envName}-2\n`,
              },
            }),
          });
        }
        await route.continue();
      });

      await app.sidebar.openEnvironment(SEED_TENANT, envA);
      const pane = app.debugPanel.erunTracePane();
      await expect(pane).toContainText(`LINE-FOR-${envA}-1`, withTestBudget());

      await app.debugPanel.erunTraceClearButton().click();
      const clearedNotice = page.getByText('Showing entries since you cleared.');
      await clearedNotice.waitFor({ state: 'visible' });
      await expect(clearedNotice).toBeVisible();

      await app.sidebar.openEnvironment(SEED_TENANT, envB);
      await clearedNotice.waitFor({ state: 'hidden' });
      await expect(clearedNotice).toBeHidden();
      await expect(pane).toContainText(`LINE-FOR-${envB}-1`, withTestBudget());
    } finally {
      removeEnvironment(SEED_TENANT, envB);
    }
  });
});
