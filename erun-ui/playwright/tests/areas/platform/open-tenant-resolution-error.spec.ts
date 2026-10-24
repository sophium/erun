import { artifactPath } from '../../../fixtures/artifacts.js';
import { test, expect } from '../../../fixtures/erunApp.js';

// Stubs the RPC to return the enriched error text, so the rendering contract is
// exercised independently of any real call path.
test.describe('open tenant-resolution error — actionable, no fake Copy action', () => {
  test('the titlebar names the operation and the recovery, with no Copy action', async ({
    app,
    page,
    seededEnv,
  }) => {
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
    await expect(app.sidebar.envOpenDot(seededEnv.tenant, seededEnv.environment)).toBeVisible();

    const enrichedMessage =
      'no tenant given for open: this call path does not fall back to the working directory or a configured default tenant — pass a tenant explicitly';

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method: string };
      if (body.method === 'CloseEnvironmentSessions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ error: enrichedMessage }),
        });
      }
      await route.continue();
    });

    await app.sidebar.envOpenDot(seededEnv.tenant, seededEnv.environment).focus();
    await app.sidebar.envOpenDot(seededEnv.tenant, seededEnv.environment).press('Enter');

    const pill = page.getByRole('alert').filter({ hasText: 'no tenant given for open' });
    await expect(pill).toBeVisible();
    await expect(pill).toContainText('pass a tenant explicitly');
    await expect(page.getByRole('button', { name: 'Copy output' })).toBeHidden();

    await page.screenshot({
      path: artifactPath('test-results/open-tenant-resolution-error-titlebar.png'),
    });
  });
});
