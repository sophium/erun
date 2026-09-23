import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// A POM convergence step must resolve to the deadline its calling test chose,
// never to a second cap the step picked for itself.
//
// `toPass({ timeout: N })` is not that: `deadlineForMatcher` takes
// `min(test deadline, now + N)` (playwright/lib/matchers/expect.js), so N is a
// second, independent bound. A test that declared 60s -- several do, and one
// declares 90s -- still fails such a step at N with the rest of its budget
// unused. That is the same mistake pages/Sidebar.ts's own comment on
// openManageDialogViaKeyboard rejects ("any N here is a number the step's own
// test never chose"), and the same one the September convergence pass fixed
// three methods up in that file by making them bare.
//
// Each case below is the reproduction, not a census: the app is made slow, at a
// named RPC, for longer than the step's old cap but inside the test's own
// budget. Pre-fix every case reds at the cap with budget left; post-fix every
// case passes at the real arrival time. The delay is injected the way
// manage-redeploy-banner.spec.ts injects its slow save -- a stubbed
// `/__erun_invoke` held for a fixed window -- so it is deterministic on a quiet
// machine rather than dependent on the node actually being contended.
//
// The holds are deliberately just past each cap (cap + ~1s): the smallest delay
// that discriminates, so the suite pays seconds here rather than the tens of
// seconds a real contended machine would.

// holdInvoke makes `method` unresponsive until the window elapses, then lets
// every pending and subsequent call through. Every call inside the window is
// delayed to the window's end rather than for a fixed duration, so a poll that
// fires twice cannot slip a fast answer between two slow ones.
async function holdInvoke(page: Page, method: string, holdMs: number): Promise<void> {
  const until = Date.now() + holdMs;
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === method) {
      const remaining = until - Date.now();
      if (remaining > 0) {
        await new Promise((resolve) => setTimeout(resolve, remaining));
      }
    }
    await route.continue();
  });
}

test.describe('sidebar POM convergence steps (#2459)', () => {
  // hoverOrchestratorRow carried `toPass({ timeout: 20_000 })`. A row that
  // arrives at 21s is a slow app, not a broken one -- and one caller declares
  // 90s precisely because its card has an outage to observe clear.
  test('a hover row that lands at 21s is waited out rather than cut off at the step cap', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    await holdInvoke(page, 'ListOrchestrators', 21_000);
    await app.reboot();
    await app.sidebar.hoverOrchestratorRow(SEED_ORCHESTRATOR);
    await expect(app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR)).toBeVisible();
  });

  // openOrchestratorDialog carried `toPass({ timeout: 25_000 })`, and its own
  // comment one paragraph above claims the opposite property ("converges up to
  // the test's own budget instead"). One caller declares 60s.
  test('an orchestrator dialog whose details button lands at 26s is not cut off at the step cap', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    await holdInvoke(page, 'ListOrchestrators', 26_000);
    await app.reboot();
    await app.sidebar.openOrchestratorDialog(SEED_ORCHESTRATOR);
    await expect(app.page.getByRole('dialog', { name: 'Edit orchestrator' })).toBeVisible();
  });

  // readOrchestratorHoverCard carried `toPass({ timeout: 25_000 })` while its
  // own mirror readEnvHoverCard is bare -- and the seed's env-hover spec is the
  // caller that had to widen its test to 60s to cope with the nested caps.
  test('a hover-card read that outlasts the step cap is bounded by the test instead', async ({
    app,
    page,
  }) => {
    test.setTimeout(60_000);
    await holdInvoke(page, 'ListOrchestrators', 26_000);
    await app.reboot();
    await app.sidebar.readOrchestratorHoverCard(SEED_ORCHESTRATOR, async (card) => {
      await expect(card).toBeVisible({ timeout: 2_000 });
    });
    await expect(app.sidebar.orchestratorHoverCard(SEED_ORCHESTRATOR)).toBeVisible();
  });

  // closeEnvironment carried `toPass({ timeout: 30_000 })` around a 2s inner
  // probe. The dot only clears once CloseEnvironmentSessions answers
  // (closeEnvironmentThunks clears the tabs after the await), so a backend that
  // takes 31s to answer is a slow close, not a failed one -- and the step's own
  // comment says it is "the whole close contract for a spec", which a bound
  // below the test's budget cannot be.
  test('a close that the backend takes 31s to answer is waited out, not cut off', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(60_000);
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);
    await expect(app.sidebar.envOpenDot(tenant, environment)).toHaveCount(1);
    await holdInvoke(page, 'CloseEnvironmentSessions', 31_000);
    await app.sidebar.closeEnvironment(tenant, environment);
    await expect(app.sidebar.envOpenDot(tenant, environment)).toHaveCount(0);
  });
});
