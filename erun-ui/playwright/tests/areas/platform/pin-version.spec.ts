import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Apply stays unavailable until the plan, naming every reference's current and
// target value, is on screen.

interface InvokeBody {
  method?: string;
  args?: unknown[];
}

const AVAILABLE = ['1.0.174', '1.0.173', '1.0.172'];

const PLAN = {
  tenant: SEED_TENANT,
  environment: SEED_ENV_ALPHA,
  target: '1.0.174',
  previous: '1.0.115',
  changed: 2,
  aligned: false,
  sites: [
    {
      kind: 'terraform-ref',
      label: 'terraform-team/dev/main.tf',
      current: '1.0.102',
      target: '1.0.174',
      aligned: false,
    },
    {
      kind: 'helm-dependency',
      label: 'team-api/Chart.yaml (erun-backend-api)',
      current: '1.0.106',
      target: '1.0.174',
      aligned: false,
    },
  ],
};

test.describe('change erun version (#744)', () => {
  test('shows the plan before applying, and applies only what was previewed', async ({
    app,
    page,
  }) => {
    const calls: string[] = [];

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      const method = body.method ?? '';
      if (method === 'ListPinnableVersions') {
        calls.push(method);
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: AVAILABLE }),
        });
      }
      if (method === 'PreviewPinVersion' || method === 'ApplyPinVersion') {
        calls.push(method);
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: PLAN }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.page.getByRole('tab', { name: 'Runtime' }).click();
    await app.page
      .getByRole('button', { name: `Change erun version for ${SEED_TENANT} / ${SEED_ENV_ALPHA}` })
      .click();

    const dialog = app.page.getByTestId('pin-version-dialog');
    await expect(dialog).toBeVisible();

    // The description must name every kind of site the engine rewrites.
    await expect(dialog).toContainText('Terraform module refs');
    await expect(dialog).toContainText('dns01_webhook_image');
    await expect(dialog).toContainText('umbrella chart');
    await expect(dialog).toContainText('build-env image tag');
    await expect(dialog).toContainText('own stock release');
    await expect(dialog).toContainText('runtime version');

    // Each read below waits on a stubbed round trip, so it carries the test's
    // budget rather than expect's 10s default.
    const versionTrigger = dialog.getByRole('combobox', { name: 'Version' });
    await expect(versionTrigger).toContainText('Latest stable', withTestBudget());

    const apply = dialog.getByRole('button', { name: 'Apply', exact: true });
    await expect(apply).toBeDisabled();

    await dialog.getByRole('button', { name: 'Preview changes' }).click();

    const plan = dialog.getByRole('table', { name: 'Pending pin changes' });
    await expect(plan).toBeVisible();
    await expect(plan).toContainText('terraform-team/dev/main.tf', withTestBudget());
    await expect(plan).toContainText('team-api/Chart.yaml (erun-backend-api)', withTestBudget());
    await expect(plan).toContainText('1.0.102', withTestBudget());
    await expect(plan).toContainText('1.0.174', withTestBudget());

    await expect(apply).toBeEnabled(withTestBudget());
    await apply.click();

    await expect(dialog.getByRole('status')).toContainText(
      'Nothing is deployed yet',
      withTestBudget(),
    );
    await expect.poll(() => calls, withTestBudget()).toContain('ApplyPinVersion');
    expect(calls.indexOf('PreviewPinVersion')).toBeLessThan(calls.indexOf('ApplyPinVersion'));
  });

  // The apply is held just past expect's 10s default, so the read must carry the
  // test's budget explicitly.
  test('an apply that answers past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    let holdUntil = 0;
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      const method = body.method ?? '';
      if (method === 'ListPinnableVersions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: AVAILABLE }),
        });
      }
      if (method === 'PreviewPinVersion') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: PLAN }),
        });
      }
      if (method === 'ApplyPinVersion') {
        // Anchored to the request, so the delay is independent of dialog open time.
        if (holdUntil === 0) {
          holdUntil = Date.now() + 12_000;
        }
        const remaining = holdUntil - Date.now();
        if (remaining > 0) {
          await new Promise((resolve) => setTimeout(resolve, remaining));
        }
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: PLAN }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.page.getByRole('tab', { name: 'Runtime' }).click();
    await app.page
      .getByRole('button', { name: `Change erun version for ${SEED_TENANT} / ${SEED_ENV_ALPHA}` })
      .click();

    const dialog = app.page.getByTestId('pin-version-dialog');
    await dialog.getByRole('button', { name: 'Preview changes' }).click();
    await expect(dialog.getByRole('button', { name: 'Apply', exact: true })).toBeEnabled(
      withTestBudget(),
    );
    await dialog.getByRole('button', { name: 'Apply', exact: true }).click();

    await expect(dialog.getByRole('status')).toContainText(
      'Nothing is deployed yet',
      withTestBudget(),
    );
  });

  test('the Version select shows the environment current pin as helper text', async ({
    app,
    page,
  }) => {
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'ListPinnableVersions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: AVAILABLE }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.page.getByRole('tab', { name: 'Runtime' }).click();
    await app.page
      .getByRole('button', { name: `Change erun version for ${SEED_TENANT} / ${SEED_ENV_ALPHA}` })
      .click();

    const dialog = app.page.getByTestId('pin-version-dialog');
    // fixtures/seedRoot.ts pins alpha to 1.0.0.
    await expect(dialog).toContainText('Currently pinned to 1.0.0.');
  });

  test('a sourceless environment with no known checkout blocks Preview, Apply and Revert', async ({
    app,
    page,
  }) => {
    const reason =
      'pw/alpha has no local checkout of its repo on this machine, and no other pw environment does either.';
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'ListPinnableVersions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: AVAILABLE }),
        });
      }
      if (body.method === 'PinRepoCheckoutStatus') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: { resolvable: false, reason } }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.page.getByRole('tab', { name: 'Runtime' }).click();
    await app.page
      .getByRole('button', { name: `Change erun version for ${SEED_TENANT} / ${SEED_ENV_ALPHA}` })
      .click();

    const dialog = app.page.getByTestId('pin-version-dialog');
    await expect(dialog.getByRole('status')).toContainText(reason);

    await expect(dialog.getByRole('button', { name: 'Preview changes' })).toBeDisabled();
    await expect(dialog.getByRole('button', { name: 'Apply', exact: true })).toBeDisabled();
    await expect(
      dialog.getByRole('button', { name: 'Revert to the previously pinned erun version' }),
    ).toBeDisabled();
  });

  test('an already-aligned environment offers nothing to apply', async ({ app, page }) => {
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'ListPinnableVersions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: AVAILABLE }),
        });
      }
      if (body.method === 'PreviewPinVersion') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({
            data: { ...PLAN, changed: 0, aligned: true, sites: [] },
          }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.page.getByRole('tab', { name: 'Runtime' }).click();
    await app.page
      .getByRole('button', { name: `Change erun version for ${SEED_TENANT} / ${SEED_ENV_ALPHA}` })
      .click();

    const dialog = app.page.getByTestId('pin-version-dialog');
    await dialog.getByRole('button', { name: 'Preview changes' }).click();

    await expect(dialog).toContainText('already on');
    await expect(dialog.getByRole('button', { name: 'Apply', exact: true })).toBeDisabled();
  });

  test('says what the plan left alone when the environment runs its own runtime image', async ({
    app,
    page,
  }) => {
    // Absent-with-a-reason and absent-because-aligned look identical in a table,
    // so the dialog must say which applies.
    const skipped = [
      "runtimeversion pw/alpha rides ghcr.io/sophium/pw-devops:1.0.134's own release line, not erun's, so pin leaves it alone; it moves on the tenant's own next build/release",
      'runtimechart oci://ghcr.io/sophium/charts/pw-devops:1.0.134 names pw-devops, not the stock erun-devops chart, so it rides its own release line; pin leaves it alone',
    ];
    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'ListPinnableVersions') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: AVAILABLE }),
        });
      }
      if (body.method === 'PreviewPinVersion') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: { ...PLAN, skipped } }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openManageDialogViaKeyboard(SEED_TENANT, SEED_ENV_ALPHA);
    await app.page.getByRole('tab', { name: 'Runtime' }).click();
    await app.page
      .getByRole('button', { name: `Change erun version for ${SEED_TENANT} / ${SEED_ENV_ALPHA}` })
      .click();

    const dialog = app.page.getByTestId('pin-version-dialog');
    await dialog.getByRole('button', { name: 'Preview changes' }).click();

    const leftAlone = dialog.getByTestId('pin-skipped');
    await expect(leftAlone).toBeVisible();
    await expect(leftAlone).toContainText('Left alone:');
    await expect(leftAlone).toContainText('runtimeversion pw/alpha rides');
    await expect(leftAlone).toContainText('runtimechart');
    await expect(dialog.getByRole('table', { name: 'Pending pin changes' })).toContainText(
      'terraform-team/dev/main.tf',
    );
  });
});
