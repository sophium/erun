import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// This spec drives the Manage dialog AI tab's per-env Claude controls — the
// default-model selector and the verbose+debug launch toggle — after a real
// boot, and cancels without saving.
//
// Harness limitation: the real `claude --model ... --verbose --debug` launch
// and the save-triggered AI-tab reopen cannot be observed headless — the
// seeded env's AI tool is an inert shell, and saving would churn the shared
// baseline config mid-suite. The launch-string composition is locked by
// TestAISessionLaunchCommand and TestResolveClaudeLaunchModel in erun-common
// plus the `erun open --app-session ai --ai --dry-run` integration goldens;
// the end+respawn flow is locked by TestEndAISessionsClosesAITabsAndEndsPodSessions
// and TestEndAISessionsSkipsVerbatimAITool in erun-ui.
test.describe('manage dialog claude launch flags', () => {
  test('Default model follows Available models; verbose+debug toggles; both reset', async ({
    app,
  }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('AI');
    await expect.poll(() => app.manageDialog.getActiveTab()).toBe('AI');

    // The seeded env carries no claude block and this spec cancels without
    // saving, so the starting state is one deterministic outcome rather than
    // whatever the dialog happens to open with: nothing ticked, verbose+debug
    // off. Asserting it also catches a spec that leaks a save into the baseline.
    await expect(app.manageDialog.claudeDefaultModelSelect()).toBeVisible();
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).not.toBeChecked();

    const fable = app.manageDialog.claudeModelCheckbox('fable');
    await expect(fable).not.toBeChecked();

    // Ticking fable under Available models is what makes it selectable as the
    // Default model — the coupling between the two controls.
    await fable.click();
    await expect(fable).toBeChecked();
    await app.manageDialog.chooseClaudeDefaultModel('fable');
    await expect.poll(() => app.manageDialog.claudeDefaultModelSelectedValue()).toBe('fable');

    // Unticking fable strands the draft selection outside the available set:
    // the field must flag it, not silently drop it (launch ignores it). The
    // draft now differs from stored config in every baseline (fable removed,
    // or the default changed), so the AI tab must show the dirty marker.
    await fable.click();
    await expect(fable).not.toBeChecked();
    await expect
      .poll(() => app.manageDialog.claudeDefaultModelSelectedValue())
      .toBe('fable (not in available models — ignored at launch)');
    await expect(app.manageDialog.tab('AI')).toHaveAttribute('aria-label', /has unsaved changes/);

    await app.manageDialog.claudeVerboseDebugCheckbox().click();
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).toBeChecked();
    await app.manageDialog.claudeVerboseDebugCheckbox().click();
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).not.toBeChecked();

    // "Default" is the first available model the session starts on (opus for
    // the seeded set), not the agent's own built-in default.
    await app.manageDialog.chooseClaudeDefaultModel('Default (opus)');
    await expect
      .poll(() => app.manageDialog.claudeDefaultModelSelectedValue())
      .toBe('Default (opus)');

    // Cancel rather than save: saving would churn the shared seeded baseline
    // mid-suite and trigger an AI-session reopen.
    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
