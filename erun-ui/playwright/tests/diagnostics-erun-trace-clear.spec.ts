import { test, expect } from '../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// "Clear" on the erun-trace pane is non-destructive: it baselines the view so
// new lines stand out, without truncating the persistent log or the Copy reads.
//
// The headless harness has no real trace.log for an inert seeded env, so these
// stub the trace RPC to drive deterministic, evolving content. The baseline
// math lives in the useErunTraceBaseline hook; here we lock the rendered view.
test.describe('diagnostics erun-trace clear', () => {
  test.beforeEach(async ({ app }) => {
    if (!(await app.debugPanel.isOpen())) {
      await app.debugPanel.toggle();
      await expect(app.debugPanel.resizeHandle()).toBeVisible();
    }
    await app.debugPanel.selectTab('erun trace');
  });

  test.afterEach(async ({ app }) => {
    if (await app.debugPanel.isOpen()) {
      await app.debugPanel.toggle();
    }
  });

  test('Clear baselines the view; new lines stand out and Show all restores', async ({
    app,
    page,
    seededEnv,
  }) => {
    // Mutated later so a poll tick delivers a line that arrived after the baseline.
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
    await expect(pane).toContainText('TRACE-OLD-1', { timeout: 10_000 });

    await app.debugPanel.erunTraceClearButton().click();
    await expect(page.getByText('Showing entries since you cleared.')).toBeVisible();
    await expect(pane).not.toContainText('TRACE-OLD-1');
    await expect(pane).toContainText('No new entries since you cleared.');

    extra = 'TRACE-NEW-1\n';
    await expect(pane).toContainText('TRACE-NEW-1', { timeout: 10_000 });
    await expect(pane).not.toContainText('TRACE-OLD-1');

    // Show all restores the full log, proving Clear hid rather than truncated it.
    await app.debugPanel.erunTraceShowAllButton().click();
    await expect(pane).toContainText('TRACE-OLD-1');
    await expect(page.getByText('Showing entries since you cleared.')).toBeHidden();
  });

  test('the baseline is per-env: switching envs resets it', async ({ app, page, seededEnv }) => {
    // Two throwaway envs (seededEnv plus one seeded inline) so opening both never
    // leaves the shared baseline rows open for other specs. Per-env trace content
    // makes a baseline that leaked across the switch visible.
    const envA = seededEnv.environment;
    const envB = uniqueEnvironmentName('per-env-reset-b');
    seedEnvironment(SEED_TENANT, envB);
    try {
      await app.sidebar
        .envRowButton(SEED_TENANT, envB)
        .waitFor({ state: 'visible', timeout: 10_000 });

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
      await expect(pane).toContainText(`LINE-FOR-${envA}-1`, { timeout: 10_000 });

      await app.debugPanel.erunTraceClearButton().click();
      await expect(page.getByText('Showing entries since you cleared.')).toBeVisible();

      await app.sidebar.openEnvironment(SEED_TENANT, envB);
      await expect(page.getByText('Showing entries since you cleared.')).toBeHidden();
      await expect(pane).toContainText(`LINE-FOR-${envB}-1`, { timeout: 10_000 });
    } finally {
      removeEnvironment(SEED_TENANT, envB);
    }
  });
});
