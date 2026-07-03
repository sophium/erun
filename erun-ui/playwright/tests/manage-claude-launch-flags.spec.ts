import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The env Manage dialog's AI tab gained a per-env Claude
// "Default model" selector (launched as `claude --model`, with `fable` newly
// selectable under Available models) and a "verbose + debug" launch toggle
// (`claude --verbose --debug`). This spec exercises the controls end-to-end
// after a real boot, relative to whatever this machine's config starts them
// at: ticking fable under Available models makes it selectable as Default
// model, the draft reflects the picks and marks the tab dirty, a selection
// that falls out of the available set is flagged rather than silently
// dropped, and resetting returns the default option. All without saving, so
// the dev's persisted config is untouched (mirrors the no-save approach in
// manage-claude-effort).
//
// Harness limitation: the real `claude --model ... --verbose --debug` launch
// and the save-triggered AI-tab reopen cannot be observed headless — the
// seeded env's AI tool is an inert shell, and saving would churn the shared
// baseline config mid-suite. The
// launch-string composition is locked by TestAISessionLaunchCommand and
// TestResolveClaudeLaunchModel in erun-common plus the `erun open
// --app-session ai --ai --dry-run` integration goldens; the end+respawn flow
// is locked by TestEndAISessionsClosesAITabsAndEndsPodSessions and
// TestEndAISessionsSkipsVerbatimAITool in erun-ui.
test.describe('manage dialog claude launch flags', () => {
  test('Default model follows Available models; verbose+debug toggles; both reset', async ({
    app,
  }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('AI');
    await expect.poll(() => app.manageDialog.getActiveTab()).toBe('AI');

    // Both fields render. The seeded env leaves the claude block unset, but
    // the spec still captures the starting values and asserts every
    // transition relative to them — that keeps it valid even if the seeded
    // baseline ever adopts defaults.
    await expect(app.manageDialog.claudeDefaultModelSelect()).toBeVisible();
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).toBeVisible();
    const initialVerbose = await app.manageDialog.claudeVerboseDebugCheckbox().isChecked();

    // fable is a known model and opt-in: it renders as an
    // Available-models checkbox.
    const fable = app.manageDialog.claudeModelCheckbox('fable');
    await expect(fable).toBeVisible();
    const initialFable = await fable.isChecked();

    // With fable ticked under Available models it becomes a Default-model
    // option — the link between the two fields — and selecting it is
    // reflected in the draft.
    if (!initialFable) {
      await fable.click();
    }
    await expect(fable).toBeChecked();
    await app.manageDialog.chooseClaudeDefaultModel('fable');
    await expect.poll(() => app.manageDialog.claudeDefaultModelSelectedValue()).toBe('fable');

    // Unticking fable strands the draft selection outside the available set:
    // the field must flag it (visibility of system status) instead of
    // silently dropping it — the launch side ignores it. After this step the
    // draft is guaranteed to differ from the stored config in every baseline
    // (fable removed, or the default changed), so the AI tab must carry the
    // dirty marker (visible affordance for the unsaved change).
    await fable.click();
    await expect(fable).not.toBeChecked();
    await expect
      .poll(() => app.manageDialog.claudeDefaultModelSelectedValue())
      .toBe('fable (not in available models — ignored at launch)');
    await expect(app.manageDialog.tab('AI')).toHaveAttribute('aria-label', /has unsaved changes/);

    // The verbose+debug launch toggle flips and flips back in the draft,
    // relative to whatever this machine's config starts it at.
    await app.manageDialog.claudeVerboseDebugCheckbox().click();
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).toBeChecked({
      checked: !initialVerbose,
    });
    await app.manageDialog.claudeVerboseDebugCheckbox().click();
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).toBeChecked({
      checked: initialVerbose,
    });

    // Resetting the Default model returns the draft to the default option.
    // "Default" names the first available model the session starts on (opus
    // for the seeded default set), not the agent's own default.
    await app.manageDialog.chooseClaudeDefaultModel('Default (opus)');
    await expect
      .poll(() => app.manageDialog.claudeDefaultModelSelectedValue())
      .toBe('Default (opus)');

    // Cancel without saving so the dev's persisted config is not mutated and
    // no live AI session is reopened.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
