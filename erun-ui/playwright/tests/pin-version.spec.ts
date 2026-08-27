import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

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

    // The regression: the Version select's trigger must show its
    // "no explicit choice" option's label, not render blank.
    const versionTrigger = dialog.getByRole('combobox', { name: 'Version' });
    await expect(versionTrigger).toContainText('Latest stable');

    // Nothing may be applied before a plan exists — that is the whole gate.
    const apply = dialog.getByRole('button', { name: 'Apply', exact: true });
    await expect(apply).toBeDisabled();

    await dialog.getByRole('button', { name: 'Preview changes' }).click();

    // The plan names each reference and both of its values, so the operator is
    // agreeing to specific edits rather than to a version number.
    const plan = dialog.getByRole('table', { name: 'Pending pin changes' });
    await expect(plan).toBeVisible();
    await expect(plan).toContainText('terraform-team/dev/main.tf');
    await expect(plan).toContainText('team-api/Chart.yaml (erun-backend-api)');
    await expect(plan).toContainText('1.0.102');
    await expect(plan).toContainText('1.0.174');

    await expect(apply).toBeEnabled();
    await apply.click();

    await expect(dialog.getByRole('status')).toContainText('Nothing is deployed yet');
    await expect.poll(() => calls).toContain('ApplyPinVersion');
    // A preview always precedes an apply.
    expect(calls.indexOf('PreviewPinVersion')).toBeLessThan(calls.indexOf('ApplyPinVersion'));
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
});
