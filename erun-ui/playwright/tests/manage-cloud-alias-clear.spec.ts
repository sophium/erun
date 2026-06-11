import { test, expect } from '../fixtures/erunApp.js';

// Regression: issue #211 — the env Manage dialog's Cloud alias dropdown had no
// way to clear a selection once set; it only offered configured aliases. The
// fix always adds a selectable "— None —" entry whenever at least one alias is
// configured, mapping to an empty cloudProviderAlias (env renders "Not
// linked"). Verified here without saving, so the dev's config is untouched.
//
// Harness note: the Cloud alias select only renders when the tenant has at
// least one cloud alias configured; otherwise an EmptyState renders. The
// headless backend reflects the dev's real ~/.erun, so the spec skips with
// justification when no tenant/env exposes the select. (Issue #442 will make
// this deterministic against a fixture HOME.)
test.describe('manage dialog cloud alias clear', () => {
  test('a configured cloud alias can be cleared via "— None —"', async ({ app }) => {
    const tenants = await app.sidebar.tenants();
    test.skip(tenants.length === 0, 'no tenants in this developer harness');

    const located = await openFirstEnvWithCloudAliasSelect(app);
    test.skip(located === null, 'no env with a cloud alias select in this harness');

    // The clear option is always offered when aliases exist.
    await app.manageDialog.openCloudAliasOptions();
    await expect(app.manageDialog.cloudAliasNoneOption()).toBeVisible();

    // Selecting it clears the draft value back to the placeholder, the
    // observable signal that cloudProviderAlias became empty. Cancel without
    // saving so the dev's persisted config is not mutated.
    await app.manageDialog.cloudAliasNoneOption().click();
    await expect.poll(() => app.manageDialog.cloudAliasSelectedValue()).toBe('Select cloud alias');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});

// openFirstEnvWithCloudAliasSelect walks the first tenant's envs, opening each
// Manage dialog until it finds one whose General tab renders the cloud-alias
// select. Returns the env name, or null when none qualifies (caller skips).
async function openFirstEnvWithCloudAliasSelect(
  app: import('../pages/index.js').AppShell,
): Promise<string | null> {
  const tenants = await app.sidebar.tenants();
  const tenant = tenants[0]!;
  const envs = await app.sidebar.environmentsFor(tenant);
  for (const env of envs) {
    // The row's edit button is pointer-events-none/opacity-0 until the row is
    // hovered, selected, or focused. Activate it via the keyboard: focusing
    // flips group-focus-within → interactive, and Enter fires the handler
    // without depending on the env being the effective selection and without
    // a hover (which would open the row tooltip and intercept a mouse click).
    await app.sidebar.openManageDialogViaKeyboard(tenant, env);
    await app.manageDialog.waitForOpen();
    if (await app.manageDialog.cloudAliasSelectVisible()) {
      return env;
    }
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  }
  return null;
}
