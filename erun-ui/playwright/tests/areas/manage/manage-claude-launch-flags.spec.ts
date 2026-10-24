import { test, expect } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The real launch can't be observed headless and saving would churn the shared
// baseline, so this cancels; launch composition is covered by Go tests.
test.describe('manage dialog claude launch flags', () => {
  test('Default model follows Available models; verbose+debug toggles; both reset', async ({
    app,
  }) => {
    await app.sidebar.openManageDialogFor(SEED_TENANT, SEED_ENV_ALPHA);
    await app.manageDialog.waitForOpen();
    await app.manageDialog.selectTab('AI');
    await expect.poll(() => app.manageDialog.getActiveTab()).toBe('AI');

    // Asserting the starting state also catches a spec that leaks a save into the baseline.
    await expect(app.manageDialog.claudeDefaultModelSelect()).toBeVisible();
    await expect(app.manageDialog.claudeVerboseDebugCheckbox()).not.toBeChecked();

    const fable = app.manageDialog.claudeModelCheckbox('fable');
    await expect(fable).not.toBeChecked();

    await fable.click();
    await expect(fable).toBeChecked();
    await app.manageDialog.chooseClaudeDefaultModel('fable');
    await expect.poll(() => app.manageDialog.claudeDefaultModelSelectedValue()).toBe('fable');

    // A stranded default must be flagged, not silently dropped.
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

    // "Default" is the first available model, not the agent's built-in default.
    await app.manageDialog.chooseClaudeDefaultModel('Default (opus)');
    await expect
      .poll(() => app.manageDialog.claudeDefaultModelSelectedValue())
      .toBe('Default (opus)');

    await app.manageDialog.cancel();
    await app.manageDialog.waitForClosed();
  });
});
