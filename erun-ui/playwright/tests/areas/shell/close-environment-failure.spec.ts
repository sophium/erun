import { test, expect } from '../../../fixtures/erunApp.js';

// A CloseEnvironmentSessions failure used to render as a neutral info pill
// via showTerminalMessage — polite aria-live, no role="alert" — indistinguishable
// from a routine status update. Unlike a Manage-dialog-scoped failure (where
// the dialog's own inline error already carries the message while the
// titlebar sits behind the modal's aria-hidden), closing an env's tabs is a
// sidebar action with no dialog in the way, so the titlebar pill is the only
// surface — and it must be an actionable error. The pill no longer offers a
// Copy action for a bare RPC error with no captured command output: copying
// the same sentence already visible in the pill earns the operator nothing.
test.describe('close environment — failure surfacing', () => {
  test('a close failure renders as an error, not a silent info pill, with no Copy action for a message with no captured output', async ({
    app,
    page,
    seededEnv,
  }) => {
    await app.sidebar.openEnvironment(seededEnv.tenant, seededEnv.environment);
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
    await expect(pill).toBeVisible();
    await expect(page.getByRole('button', { name: 'Copy output' })).toBeHidden();

    // The close never completed, so the dot must still be there — a stray
    // success side effect alongside the reported failure would itself be a bug.
    await expect(app.sidebar.envOpenDot(seededEnv.tenant, seededEnv.environment)).toBeVisible();
  });
});
