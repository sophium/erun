import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// Changing an environment's erun version rewrites files across the tenant repo,
// so the contract this locks is that the operator sees the plan before anything
// is written: Apply stays unavailable until a plan is on screen, and the plan
// names every reference with its current and target value. The rewriting itself
// is covered by the Go tests; what only the UI can show is the gate.

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

    // The description claims it re-pins "every erun reference", so it has to
    // name every kind of site the engine rewrites. It once listed four of the
    // six, so an operator would believe a re-pin could not have touched a
    // coordinate it did. erun-common's pin_surface_drift_test.go is the
    // authoritative enumeration guard across all the describing surfaces; this
    // is the rendered dialog carrying the same enumeration.
    await expect(dialog).toContainText('Terraform module refs');
    await expect(dialog).toContainText('dns01_webhook_image');
    await expect(dialog).toContainText('umbrella chart');
    await expect(dialog).toContainText('build-env image tag');
    await expect(dialog).toContainText('own stock release');
    await expect(dialog).toContainText('runtime version');

    // The regression: the Version select's trigger must show its
    // "no explicit choice" option's label, not render blank.
    // Every content read below is the answer to one of this spec's own stubbed
    // round trips -- the pinnable versions, the preview, the apply -- rather
    // than a render that has already happened. `expect(...)` and `expect.poll`
    // carry expect's 10s default with no explicit timeout, which is a separate
    // clock from the 30s this test declares; each read therefore names that
    // budget. See the held-apply case at the end of this file.
    const versionTrigger = dialog.getByRole('combobox', { name: 'Version' });
    await expect(versionTrigger).toContainText('Latest stable', withTestBudget());

    // Nothing may be applied before a plan exists — that is the whole gate.
    const apply = dialog.getByRole('button', { name: 'Apply', exact: true });
    await expect(apply).toBeDisabled();

    await dialog.getByRole('button', { name: 'Preview changes' }).click();

    // The plan names each reference and both of its values, so the operator is
    // agreeing to specific edits rather than to a version number.
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
    // A preview always precedes an apply.
    expect(calls.indexOf('PreviewPinVersion')).toBeLessThan(calls.indexOf('ApplyPinVersion'));
  });

  // The apply's own answer is what the status read below is about, and a bare
  // `toContainText` resolves to expect's 10s default rather than to the budget
  // this test declared. The hold is injected at the RPC this spec already stubs
  // -- rather than by loading the machine -- so the reproduction is
  // deterministic on a quiet host, and it sits just past that 10s default: the
  // smallest delay that discriminates.
  //
  // Pre-fix this reds at exactly 10_000ms with 20s of its own budget unspent;
  // with the read pointed at the budget the test declared it passes at the
  // apply's real arrival.
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
        // Anchored to the request, so the delay is the same however long the
        // dialog took to open, and held before the response is written.
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
    // The seeded alpha env is pinned to 1.0.0 (fixtures/seedRoot.ts
    // seedEnvironment) — that fact, not an abstract "no choice made" label, is
    // what the operator actually wants from this control.
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
    // The row an operator would otherwise find missing: an environment whose
    // runtime image is not erun's own keeps its runtimeversion, so the plan
    // carries no site for it. Absent-with-a-reason and absent-because-aligned
    // look identical in a table, so the dialog has to say which one this is.
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
    // The erun-owned references still move, so this is a reason and not a
    // blocked plan.
    await expect(dialog.getByRole('table', { name: 'Pending pin changes' })).toContainText(
      'terraform-team/dev/main.tf',
    );
  });
});
