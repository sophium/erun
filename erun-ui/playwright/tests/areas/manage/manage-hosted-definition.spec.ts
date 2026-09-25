import { expect, test } from '../../../fixtures/erunApp.js';
import {
  SEED_ENV_ALPHA,
  SEED_ENV_DELTA,
  SEED_HOSTED_API_HOST,
  SEED_HOSTED_ENVIRONMENT_ID,
  SEED_HOSTED_REVISION,
  SEED_HOSTED_TENANT_ID,
  SEED_TENANT,
} from '../../../fixtures/seedRoot.js';

// The Hosted environment panel is the desktop's whole surface for a hosted
// definition: which platform row this local environment corresponds to, and —
// behind one read-only button — whether the platform's copy has moved past what
// this machine last pulled.
//
// What only the rendered UI can show, and therefore what this locks: the panel
// appears for a hosted env and is absent for an unhosted one, the panel names
// the row rather than three opaque ids, the comparison is only ever made on the
// operator's own action (never on open), and when the platform is ahead the
// panel says so and names the command that brings it down instead of offering a
// button that would write.

interface InvokeBody {
  method?: string;
  args?: unknown[];
}

function hostedDrift(behind: boolean): Record<string, unknown> {
  return {
    localRevision: SEED_HOSTED_REVISION,
    platformRevision: behind ? SEED_HOSTED_REVISION + 2 : SEED_HOSTED_REVISION,
    behind,
    describe: behind
      ? `the platform is at definition revision ${SEED_HOSTED_REVISION + 2} and this machine last pulled revision ${SEED_HOSTED_REVISION}`
      : `this machine is up to date at definition revision ${SEED_HOSTED_REVISION}`,
  };
}

test.describe('manage dialog hosted environment panel', () => {
  test('names the platform row, and checks for updates only when asked', async ({ app, page }) => {
    const calls: string[] = [];
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'CheckHostedDefinitionDrift') {
        calls.push(body.method);
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: hostedDrift(true) }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_DELTA);
    await app.manageDialog.waitForOpen();

    // The General tab is where an operator already reads managed-cloud; the
    // marker sits beside it rather than in a tab of its own.
    await expect(app.manageDialog.hostedPanel()).toBeVisible();
    await expect(app.manageDialog.hostedPanel()).toContainText(SEED_HOSTED_API_HOST);
    await expect(app.manageDialog.hostedPanel()).toContainText(SEED_HOSTED_ENVIRONMENT_ID);
    await expect(app.manageDialog.hostedPanel()).toContainText(SEED_HOSTED_TENANT_ID);

    // Opening the dialog is not a reason to contact the platform: the
    // comparison is the operator's own action, never a poll. Nothing here
    // writes, so nothing here may happen unasked.
    expect(calls).toEqual([]);

    await app.manageDialog.hostedCheckButton().click();
    await expect.poll(() => calls).toEqual(['CheckHostedDefinitionDrift']);

    // The platform is ahead, so the panel says so and names the command that
    // brings the definition down -- the desktop does not pull on its own.
    await expect(app.manageDialog.hostedDriftLine()).toContainText('erun platform env pull');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('is absent for an environment that carries no hosted marker', async ({ app }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    await expect(app.manageDialog.hostedPanel()).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
