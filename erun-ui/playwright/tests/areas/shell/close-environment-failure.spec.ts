import { test, expect } from '../../../fixtures/erunApp.js';

// Closing an env's tabs has no dialog in the way, so a failure must surface as an actionable
// titlebar alert.
test.describe('close environment — failure surfacing', () => {
  test('a close failure renders as an error, not a silent info pill, with no Copy action for a message with no captured output', async ({
    app,
    page,
    seededEnv,
  }) => {
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await app.sidebar
      .envOpenDot(seededEnv.tenant, seededEnv.environment)
      .waitFor({ state: 'visible' });
    await expect(app.sidebar.envOpenDot(seededEnv.tenant, seededEnv.environment)).toBeVisible();

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'CloseEnvironmentSessions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ error: 'CLOSE_ENVIRONMENT_UNREACHABLE_MARKER' }),
        });
      }
      await route.continue();
    });

    await app.sidebar.envOpenDot(seededEnv.tenant, seededEnv.environment).focus();
    await app.sidebar.envOpenDot(seededEnv.tenant, seededEnv.environment).press('Enter');

    const pill = page
      .getByRole('alert')
      .filter({ hasText: 'CLOSE_ENVIRONMENT_UNREACHABLE_MARKER' });
    await pill.waitFor({ state: 'visible' });
    await expect(pill).toBeVisible();
    await expect(page.getByRole('button', { name: 'Copy output' })).toBeHidden();

    // The close never completed, so the open dot must remain.
    await expect(app.sidebar.envOpenDot(seededEnv.tenant, seededEnv.environment)).toBeVisible();
  });
});
