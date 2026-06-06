import { test, expect } from '../fixtures/erunApp.js';

// Issue #440 — the sidebar "Upgrade all" button opens a preview dialog that
// resolves the cross-env upgrade plan (every opted-in env, its channel, and
// current → target) before any deploy. This spec drives the reachable surface:
// the button opens the dialog, the dialog renders either the plan table or the
// "no environments opted in" empty state, and Cancel closes it without
// deploying. The populated-plan path (lagging envs → confirm → deploy) depends
// on opted-in envs in ~/.erun and on a registry lookup, so it is verified
// end-to-end against a fixture in the PR; the CLI dry-run goldens own the plan
// resolution + deploy composition.
test.describe('sidebar Upgrade all', () => {
  test('the Upgrade all button opens the preview dialog and cancels cleanly', async ({ app }) => {
    await app.sidebar.openUpgradeAll();

    const dialog = app.sidebar.upgradeAllDialog();
    await expect(dialog).toBeVisible({ timeout: 6_000 });

    // The body resolves to one of the two terminal states (never stuck on the
    // loading spinner): a plan table, or the opted-in empty state.
    await expect
      .poll(async () => {
        const hasTable = await dialog.getByRole('table', { name: 'Upgrade plan' }).count();
        const hasEmpty = await dialog
          .getByText('No environments are opted into Upgrade all')
          .count();
        return hasTable + hasEmpty;
      })
      .toBeGreaterThan(0);

    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
  });
});
