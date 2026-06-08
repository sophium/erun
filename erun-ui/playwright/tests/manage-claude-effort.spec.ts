import { test, expect } from '../fixtures/erunApp.js';

// Issue #469 — the env Manage dialog's AI tab gained a per-env Claude "Effort"
// selector (low|medium|high|xhigh|max) that the desktop injects as
// `claude --effort` when launching the AI tab. This spec exercises the control
// end-to-end after a real boot: it renders, defaults to "Default (max)", an
// override is reflected in the draft and marks the tab dirty, and resetting to
// default returns the draft. All without saving, so the dev's persisted config
// is untouched (mirrors the no-save approach in manage-cloud-alias-clear).
//
// Harness note: the real `claude --effort` launch cannot run headless, so the
// observable invariant here is the persisted-draft value of the selector (the
// #331 pattern). The launch-string composition and the max fallback for
// unset/invalid values are covered by the Go test TestAILaunchCommandGuardsClaudeOnly
// and TestResolveClaudeEffort in erun-ui.
test.describe('manage dialog claude effort', () => {
  test('Effort selector defaults to max, overrides, and resets', async ({ app }) => {
    const tenants = await app.sidebar.tenants();
    test.skip(tenants.length === 0, 'no tenants in this developer harness');
    const tenant = tenants[0]!;
    const envs = await app.sidebar.environmentsFor(tenant);
    test.skip(envs.length === 0, 'no environments in this developer harness');
    const env = envs[0]!;

    await app.sidebar.openManageDialogFor(tenant, env);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('AI');
    await expect.poll(() => app.manageDialog.getActiveTab()).toBe('AI');

    // Unset → the selector shows the default level (max), proving the backend
    // default reaches the field and the field is resettable to it.
    await expect(app.manageDialog.claudeEffortSelect()).toBeVisible();
    await expect.poll(() => app.manageDialog.claudeEffortSelectedValue()).toBe('Default (max)');

    // Overriding to a concrete level is reflected in the draft and marks the
    // AI tab dirty (visible affordance for the unsaved change).
    await app.manageDialog.chooseClaudeEffort('low');
    await expect.poll(() => app.manageDialog.claudeEffortSelectedValue()).toBe('low');
    await expect(app.manageDialog.tab('AI')).toHaveAttribute('aria-label', /has unsaved changes/);

    // Resetting to default returns the draft to "Default (max)".
    await app.manageDialog.chooseClaudeEffort('Default (max)');
    await expect.poll(() => app.manageDialog.claudeEffortSelectedValue()).toBe('Default (max)');

    // Cancel without saving so the dev's persisted config is not mutated.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
