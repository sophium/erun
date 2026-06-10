import { test, expect } from '../fixtures/erunApp.js';

// Issues #482/#477 — the env Manage dialog's AI tab gained a per-env Claude
// "Default model" selector (launched as `claude --model`, with `fable` newly
// selectable under Available models) and a "verbose + debug" launch toggle
// (`claude --verbose --debug`). This spec exercises the controls end-to-end
// after a real boot: both render with their defaults, ticking fable under
// Available models makes it selectable as Default model, the draft reflects
// the picks and marks the tab dirty, a stored model that falls out of the
// available set is flagged rather than silently dropped, and resetting
// returns the defaults. All without saving, so the dev's persisted config is
// untouched (mirrors the no-save approach in manage-claude-effort).
//
// Harness limitation: the real `claude --model ... --verbose --debug` launch
// and the save-triggered AI-tab reopen cannot be observed headless — saving
// here would mutate the dev's config and end their real AI sessions. The
// launch-string composition is locked by TestAISessionLaunchCommand and
// TestResolveClaudeDefaultModel in erun-common plus the `erun open
// --app-session ai --ai --dry-run` integration goldens; the end+respawn flow
// is locked by TestEndAISessionsClosesAITabsAndEndsPodSessions and
// TestEndAISessionsSkipsVerbatimAITool in erun-ui.
test.describe('manage dialog claude launch flags', () => {
  test('Default model follows Available models; verbose+debug toggles; both reset', async ({
    app,
  }) => {
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

    // Both new fields render with their unset defaults. The fields are brand
    // new, so no developer config can carry an override yet.
    await expect(app.manageDialog.claudeDefaultModelSelect()).toBeVisible();
    await expect
      .poll(() => app.manageDialog.claudeDefaultModelSelectedValue())
      .toBe('Default (Claude decides)');
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).toBeVisible();
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).not.toBeChecked();

    // fable is a known model (issue #482) and opt-in: it renders as an
    // Available-models checkbox, unticked by default.
    const fable = app.manageDialog.claudeModelCheckbox('fable');
    await expect(fable).toBeVisible();
    await expect(fable).not.toBeChecked();

    // Ticking fable under Available models makes it a Default-model option —
    // the link between the two fields — and selecting it is reflected in the
    // draft and marks the AI tab dirty (visible affordance for the unsaved
    // change).
    await fable.click();
    await expect(fable).toBeChecked();
    await app.manageDialog.chooseClaudeDefaultModel('fable');
    await expect.poll(() => app.manageDialog.claudeDefaultModelSelectedValue()).toBe('fable');
    await expect(app.manageDialog.tab('AI')).toHaveAttribute('aria-label', /has unsaved changes/);

    // Unticking fable strands the stored selection outside the available set:
    // the field must flag it (visibility of system status) instead of
    // silently dropping it — the launch side ignores it.
    await fable.click();
    await expect(fable).not.toBeChecked();
    await expect
      .poll(() => app.manageDialog.claudeDefaultModelSelectedValue())
      .toBe('fable (not in available models — ignored at launch)');

    // The verbose+debug launch toggle flips on and back off in the draft.
    await app.manageDialog.claudeVerboseDebugCheckbox().click();
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).toBeChecked();
    await app.manageDialog.claudeVerboseDebugCheckbox().click();
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).not.toBeChecked();

    // Resetting the Default model returns the draft to the default option.
    await app.manageDialog.chooseClaudeDefaultModel('Default (Claude decides)');
    await expect
      .poll(() => app.manageDialog.claudeDefaultModelSelectedValue())
      .toBe('Default (Claude decides)');

    // Cancel without saving so the dev's persisted config is not mutated and
    // no live AI session is reopened.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
