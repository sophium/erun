import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The real claude launch can't run headless, so only the draft value is checked here.
test.describe('manage dialog claude effort', () => {
  test('Effort selector defaults to ultracode, overrides, and resets', async ({ app }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('AI');
    await expect.poll(() => app.manageDialog.getActiveTab()).toBe('AI');

    await expect(app.manageDialog.claudeEffortSelect()).toBeVisible();
    await expect
      .poll(() => app.manageDialog.claudeEffortSelectedValue())
      .toBe('Default (ultracode)');

    await app.manageDialog.chooseClaudeEffort('low');
    await expect.poll(() => app.manageDialog.claudeEffortSelectedValue()).toBe('low');
    await expect(app.manageDialog.tab('AI')).toHaveAttribute('aria-label', /has unsaved changes/);

    // Distinct from the "Default (ultracode)" inherit entry.
    await app.manageDialog.chooseClaudeEffort('ultracode');
    await expect.poll(() => app.manageDialog.claudeEffortSelectedValue()).toBe('ultracode');

    await app.manageDialog.chooseClaudeEffort('Default (ultracode)');
    await expect
      .poll(() => app.manageDialog.claudeEffortSelectedValue())
      .toBe('Default (ultracode)');

    // Cancel so the persisted config is not mutated.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
