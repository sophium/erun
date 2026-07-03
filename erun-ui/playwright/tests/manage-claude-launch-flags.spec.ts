import { test, expect } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

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

    // Assert every transition relative to the captured starting values, so
    // the spec stays valid even if the seeded baseline ever adopts claude
    // defaults.
    await expect(app.manageDialog.claudeDefaultModelSelect()).toBeVisible();
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).toBeVisible();
    const initialVerbose = await app.manageDialog.claudeVerboseDebugCheckbox().isChecked();

    const fable = app.manageDialog.claudeModelCheckbox('fable');
    await expect(fable).toBeVisible();
    const initialFable = await fable.isChecked();

    // Ticking fable under Available models is what makes it selectable as the
    // Default model — the coupling between the two controls.
    if (!initialFable) {
      await fable.click();
    }
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
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).toBeChecked({
      checked: !initialVerbose,
    });
    await app.manageDialog.claudeVerboseDebugCheckbox().click();
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).toBeChecked({
      checked: initialVerbose,
    });

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
