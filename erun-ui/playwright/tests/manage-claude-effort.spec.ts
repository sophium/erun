import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The Manage dialog's AI tab carries a per-env Claude "Effort" selector.
//
// Harness note: the real claude launch cannot run headless, so the observable
// invariant here is the persisted-draft value of the selector. The launch-string
// composition (--effort vs --settings) and the ultracode fallback are covered by
// the Go tests TestAISessionLaunchCommand* and TestResolveClaudeEffort in erun-common.
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

    // ultracode is also selectable as an explicit level (distinct from the
    // "Default (ultracode)" inherit entry).
    await app.manageDialog.chooseClaudeEffort('ultracode');
    await expect.poll(() => app.manageDialog.claudeEffortSelectedValue()).toBe('ultracode');

    await app.manageDialog.chooseClaudeEffort('Default (ultracode)');
    await expect
      .poll(() => app.manageDialog.claudeEffortSelectedValue())
      .toBe('Default (ultracode)');

    // Cancel without saving so the dev's persisted config is not mutated.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
