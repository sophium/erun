import { expect, test, waitForSeededRow } from '../../../fixtures/erunApp.js';
import {
  SEED_ENV_ALPHA,
  SEED_ENV_DELTA,
  SEED_HOSTED_API_HOST,
  SEED_HOSTED_ENVIRONMENT_ID,
  SEED_HOSTED_REVISION,
  SEED_HOSTED_STALE_DIGEST,
  SEED_HOSTED_TENANT_ID,
  SEED_TENANT,
  removeEnvironment,
  seedHostedEnvironment,
} from '../../../fixtures/seedRoot.js';

// "In step" can't be staged without reimplementing the projection digest; that
// branch is covered in erun-ui/internal/settings/hosted_definition_upload_test.go.

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

function hostedUpload(revision: number): Record<string, unknown> {
  return {
    revision,
    describe: `uploaded ${SEED_TENANT}/${SEED_ENV_DELTA} to ${SEED_HOSTED_ENVIRONMENT_ID} as definition revision ${revision}`,
    localChange: {
      available: true,
      changed: false,
      describe: "this machine's settings match what was last sent to the platform",
    },
  };
}

test.describe('manage dialog hosted environment panel', () => {
  // Staged per test: other specs count the seeded environment population.
  test.beforeEach(async ({ app }) => {
    seedHostedEnvironment(SEED_TENANT, SEED_ENV_DELTA);
    await waitForSeededRow(app, SEED_TENANT, SEED_ENV_DELTA);
  });

  test.afterEach(() => {
    removeEnvironment(SEED_TENANT, SEED_ENV_DELTA);
  });

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

    await expect(app.manageDialog.hostedPanel()).toBeVisible();
    await expect(app.manageDialog.hostedPanel()).toContainText(SEED_HOSTED_API_HOST);
    await expect(app.manageDialog.hostedPanel()).toContainText(SEED_HOSTED_ENVIRONMENT_ID);
    await expect(app.manageDialog.hostedPanel()).toContainText(SEED_HOSTED_TENANT_ID);

    // Opening the dialog must not contact the platform.
    expect(calls).toEqual([]);

    await app.manageDialog.hostedCheckButton().click();
    await expect.poll(() => calls).toEqual(['CheckHostedDefinitionDrift']);

    // The desktop does not pull on its own.
    await expect(app.manageDialog.hostedDriftLine()).toContainText('erun platform env pull');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // A marked sibling in the same tenant makes this absence mean "not hosted".
  test('is absent for an environment that carries no hosted marker', async ({ app }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    await expect(app.manageDialog.hostedPanel()).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('shows this machine has moved, and uploads it on request', async ({ app, page }) => {
    // A stale digest is the state a change made while the desktop was closed leaves.
    removeEnvironment(SEED_TENANT, SEED_ENV_DELTA);
    seedHostedEnvironment(SEED_TENANT, SEED_ENV_DELTA, {
      definitionDigest: SEED_HOSTED_STALE_DIGEST,
    });
    await waitForSeededRow(app, SEED_TENANT, SEED_ENV_DELTA);

    const uploads: InvokeBody[] = [];
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'UploadHostedDefinition') {
        uploads.push(body);
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: hostedUpload(SEED_HOSTED_REVISION + 1) }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_DELTA);
    await app.manageDialog.waitForOpen();

    await expect(app.manageDialog.hostedLocalChangeLine()).toContainText('have changed');
    expect(uploads).toEqual([]);

    await app.manageDialog.hostedUploadButton().click();
    await expect.poll(() => uploads.length).toEqual(1);
    expect(uploads.map((call) => call.args)).toEqual([[SEED_TENANT, SEED_ENV_DELTA]]);

    await expect(app.manageDialog.hostedLocalChangeLine()).toContainText(
      `definition revision ${SEED_HOSTED_REVISION + 1}`,
    );

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('a failed upload names the command instead of leaving a dead end', async ({ app, page }) => {
    removeEnvironment(SEED_TENANT, SEED_ENV_DELTA);
    seedHostedEnvironment(SEED_TENANT, SEED_ENV_DELTA, {
      definitionDigest: SEED_HOSTED_STALE_DIGEST,
    });
    await waitForSeededRow(app, SEED_TENANT, SEED_ENV_DELTA);

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'UploadHostedDefinition') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ error: 'tenant platform is not ready (not-connected)' }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_DELTA);
    await app.manageDialog.waitForOpen();

    await app.manageDialog.hostedUploadButton().click();

    const failure = app.manageDialog.hostedDriftError();
    await expect(failure).toContainText("Cannot upload this environment's settings");
    await expect(failure).toContainText('not-connected');
    await expect(failure).toContainText(`erun platform env push ${SEED_TENANT} ${SEED_ENV_DELTA}`);
    await expect(app.manageDialog.hostedLocalChangeLine()).toContainText('have changed');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('an upload performed elsewhere refreshes the panel without reopening it', async ({
    app,
    page,
  }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_DELTA);
    await app.manageDialog.waitForOpen();
    await expect(app.manageDialog.hostedPanel()).toContainText(
      `definition revision ${SEED_HOSTED_REVISION}`,
    );

    // The dialog's config query isn't refetched by the upload's own write, so only
    // the announcement keeps the revision current.
    const recorded = SEED_HOSTED_REVISION + 3;
    seedHostedEnvironment(SEED_TENANT, SEED_ENV_DELTA, { definitionRevision: recorded });
    // The spec's identifiers don't exist in the page context, so the payload is passed in.
    await page.evaluate(
      ({ tenant, environment, revision }) => {
        const { runtime } = window as unknown as {
          runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
        };
        runtime.EventsEmit('hosted-definition-uploaded', { tenant, environment, revision });
      },
      { tenant: SEED_TENANT, environment: SEED_ENV_DELTA, revision: recorded },
    );

    await expect(app.manageDialog.hostedPanel()).toContainText(`definition revision ${recorded}`);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
