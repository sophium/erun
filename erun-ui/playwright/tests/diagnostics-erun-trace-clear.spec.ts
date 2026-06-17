import { test, expect } from '../fixtures/erunApp.js';
import {
  SEED_TENANT,
  removeEnvironment,
  seedEnvironment,
  uniqueEnvironmentName,
} from '../fixtures/seedRoot.js';

// #529: the erun-trace pane gains a non-destructive "Clear" that baselines the
// view — it hides everything currently shown so new lines stand out, without
// truncating the persistent log or the Copy / Copy-report reads.
//
// The headless harness can't populate a real trace.log for an inert seeded env
// (capture depends on which commands ran, #508/#483), so these stub the
// LoadEnvTrace RPC over /__erun_invoke to drive deterministic, evolving
// content — the same technique sidebar-upgrade-all.spec.ts uses for
// ResolveUpgradePlan. The baseline math itself (suffix vs rotation fallback)
// is owned by the unit-level hook useErunTraceBaseline; here we lock the
// rendered behaviour end to end.
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
    // Mutable so a later poll tick can deliver a line that arrived "after" the
    // baseline; every other RPC passes through untouched.
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

    // Selecting the env drives the erun-trace pane (the console is already open
    // on the erun-trace tab from beforeEach).
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    const pane = app.debugPanel.erunTracePane();
    await expect(pane).toContainText('TRACE-OLD-1', { timeout: 10_000 });

    // Clear baselines the view: the pre-clear lines vanish, the since-cleared
    // notice explains why, and (nothing new yet) the pane says so.
    await app.debugPanel.erunTraceClearButton().click();
    await expect(page.getByText('Showing entries since you cleared.')).toBeVisible();
    await expect(pane).not.toContainText('TRACE-OLD-1');
    await expect(pane).toContainText('No new entries since you cleared.');

    // A line that arrives after the baseline stands out; the pre-clear lines
    // stay hidden.
    extra = 'TRACE-NEW-1\n';
    await expect(pane).toContainText('TRACE-NEW-1', { timeout: 10_000 });
    await expect(pane).not.toContainText('TRACE-OLD-1');

    // Show all is reversible and non-destructive: the full log returns, proving
    // Clear never truncated the underlying content (Copy / Copy report read the
    // same full content, never the cleared view).
    await app.debugPanel.erunTraceShowAllButton().click();
    await expect(pane).toContainText('TRACE-OLD-1');
    await expect(page.getByText('Showing entries since you cleared.')).toBeHidden();
  });

  test('the baseline is per-env: switching envs resets it', async ({ app, page, seededEnv }) => {
    // Two throwaway envs (seededEnv plus one seeded inline) so opening both
    // never leaves the shared baseline rows open for other specs. Per-env
    // content keyed off the LoadEnvTrace args makes a leaked cut point visible.
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

      // Switching to a different env resets the baseline — the cleared state
      // must not carry across, and the new env's content shows in full.
      await app.sidebar.openEnvironment(SEED_TENANT, envB);
      await expect(page.getByText('Showing entries since you cleared.')).toBeHidden();
      await expect(pane).toContainText(`LINE-FOR-${envB}-1`, { timeout: 10_000 });
    } finally {
      removeEnvironment(SEED_TENANT, envB);
    }
  });
});
