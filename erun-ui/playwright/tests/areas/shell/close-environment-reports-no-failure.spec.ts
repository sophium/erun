import type { Page } from '@playwright/test';

import type { SeededEnvironment } from '../../../fixtures/erunApp.js';
import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_TENANT } from '../../../fixtures/seedRoot.js';
import type { AppShell } from '../../../pages/AppShell.js';

// Closing an env kills its PTYs; the resulting exits must not be reported as failures.
// Each test holds the close RPC response open so the exit lands while the close is in flight.

async function bannersAfterClose(
  app: AppShell,
  page: Page,
  seededEnv: SeededEnvironment,
  focusTab: string,
): Promise<string[]> {
  await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
  await app.tabStrip.waitForTab(focusTab);
  await app.tabStrip.tab(focusTab).click();

  let closeRequests = 0;
  let releaseClose!: () => void;
  const closeReleased = new Promise<void>((resolve) => {
    releaseClose = resolve;
  });
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method !== 'CloseEnvironmentSessions') {
      await route.continue();
      return;
    }
    closeRequests += 1;
    // Let the backend run the close (emitting the exits) while withholding its response.
    const response = await route.fetch();
    await closeReleased;
    await route.fulfill({ response });
  });

  await app.titlebar.recordBanners();

  // The control sits in a tooltip that focus opens, so retry until the close request is seen.
  await expect(async () => {
    if (closeRequests === 0) {
      await app.sidebar.activateEnvCloseControl(SEED_TENANT, seededEnv.environment);
    }
    expect(closeRequests).toBeGreaterThan(0);
  }).toPass();

  // The ERun tab's removal commits together with any notice, so waiting on it bounds the assertion.
  await app.tabStrip.tab('ERun').waitFor({ state: 'hidden' });

  const banners = await page.evaluate(
    () => (window as unknown as { __erunBannerLog?: string[] }).__erunBannerLog ?? [],
  );

  releaseClose();
  // Proves the close really happened.
  await expect(app.sidebar.envOpenDot(SEED_TENANT, seededEnv.environment)).toHaveCount(
    0,
    withTestBudget(),
  );
  return banners;
}

test.describe('close environment — a successful close reports no failure', () => {
  // The ERun tab carries the env's openSelection.
  test('the PTY exit the close itself caused is not rendered as a failure', async ({
    app,
    page,
    seededEnv,
  }) => {
    const banners = await bannersAfterClose(app, page, seededEnv, 'ERun');
    expect(banners.filter((banner) => banner.includes('signal: killed'))).toEqual([]);
  });

  // Local and AI tabs carry no openSelection, so their exit reaches the feedback path unwrapped.
  test('a close reports nothing for the tab that carries no open selection', async ({
    app,
    page,
    seededEnv,
  }) => {
    const banners = await bannersAfterClose(app, page, seededEnv, 'Local');
    expect(banners.filter((banner) => banner.includes('signal: killed'))).toEqual([]);
  });
});
