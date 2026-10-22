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

// The Hosted environment panel is the desktop's whole surface for a hosted
// definition: which platform row this local environment corresponds to,
// whether this machine's own settings have moved since they were last sent,
// and — behind one button — whether the platform's copy has moved past what
// this machine last pulled.
//
// What only the rendered UI can show, and therefore what this locks: the panel
// appears for a hosted env and is absent for an unhosted one, the panel names
// the row rather than three opaque ids, the local divergence is resolved
// without contacting the platform at all, the comparison is only ever made on
// the operator's own action (never on open), and when the platform is ahead the
// panel says so and names the command that brings it down instead of offering a
// button that would write.
//
// The one divergence state this suite cannot stage is "in step": a match
// requires the real digest of the seeded environment's portable projection, and
// a fixture that reproduced it would be a second implementation of the thing
// under test. `TestAHostOwnedChangeIsNotUploaded` and
// `TestPushStampsTheDigestOfWhatItSent` (erun-ui/hosted_definition_upload_test.go)
// own that branch, including that no upload control is offered for it.

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
  // The hosted environment belongs to this spec, not to the suite's baseline.
  // The seeded environment population is itself an assertion other specs make
  // (the titlebar's select-all shortcuts count it), so a fourth baseline
  // environment would change the subject under test for every spec that never
  // asked for one. Stage it here, for the length of one test, and take it away
  // again — the same lifecycle the seededEnv fixture applies to its own env.
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

    // The General tab is where an operator already reads managed-cloud; the
    // marker sits beside it rather than in a tab of its own.
    await expect(app.manageDialog.hostedPanel()).toBeVisible();
    await expect(app.manageDialog.hostedPanel()).toContainText(SEED_HOSTED_API_HOST);
    await expect(app.manageDialog.hostedPanel()).toContainText(SEED_HOSTED_ENVIRONMENT_ID);
    await expect(app.manageDialog.hostedPanel()).toContainText(SEED_HOSTED_TENANT_ID);

    // Opening the dialog is not a reason to contact the platform. Both halves
    // of the panel prove it here: the drift comparison is the operator's own
    // action, and the local divergence needs no platform read at all — it is
    // the only reason a change made while the desktop was closed is visible on
    // a machine that has never signed in.
    expect(calls).toEqual([]);

    await app.manageDialog.hostedCheckButton().click();
    await expect.poll(() => calls).toEqual(['CheckHostedDefinitionDrift']);

    // The platform is ahead, so the panel says so and names the command that
    // brings the definition down -- the desktop does not pull on its own.
    await expect(app.manageDialog.hostedDriftLine()).toContainText('erun platform env pull');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  // The beforeEach stages delta for this test too, deliberately: alpha is
  // unmarked while a marked sibling sits in the same tenant, so the absence
  // below reads as "this environment is not hosted" rather than the weaker
  // "nothing here is".
  test('is absent for an environment that carries no hosted marker', async ({ app }) => {
    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();

    await expect(app.manageDialog.hostedPanel()).toHaveCount(0);

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });

  test('shows this machine has moved, and uploads it on request', async ({ app, page }) => {
    // Re-stage delta with a digest its own settings no longer match, which is
    // the state a change made while the desktop was closed leaves behind.
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

    // The divergence is a fact about this machine's own file, so it is on
    // screen the moment the panel is -- before anything has been asked of the
    // platform, and before any button is pressed.
    await expect(app.manageDialog.hostedLocalChangeLine()).toContainText('have changed');
    expect(uploads).toEqual([]);

    await app.manageDialog.hostedUploadButton().click();
    await expect.poll(() => uploads.length).toEqual(1);
    expect(uploads.map((call) => call.args)).toEqual([[SEED_TENANT, SEED_ENV_DELTA]]);

    // The upload reports what it read back from disk, which is more current
    // than the marker the panel was rendered with.
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

    // An attempted failure is an alert, and it carries the recovery action:
    // the same upload from a terminal, against this environment. The standing
    // divergence is still on screen beside it, so the state that prompted the
    // click is not lost with the failure.
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

    // A watcher-driven upload writes the marker and announces it. The dialog
    // renders the marker from the environment-config query, which the
    // `environments-changed` tick the upload's own write fires does not
    // refetch -- so without the announcement the operator would be looking at
    // the revision the environment had before the upload, in the one dialog
    // whose subject is that revision.
    const recorded = SEED_HOSTED_REVISION + 3;
    seedHostedEnvironment(SEED_TENANT, SEED_ENV_DELTA, { definitionRevision: recorded });
    // The emit itself is the browser's, exactly as the desktop's own Wails
    // event is: runtime.EventsEmit is what the headless shim posts to
    // /__erun_emit. The spec's own identifiers do not exist in that context,
    // so everything the payload carries is passed in.
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
