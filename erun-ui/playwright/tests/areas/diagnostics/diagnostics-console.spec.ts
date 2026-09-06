import { test, expect } from '../../../fixtures/erunApp.js';

// Diagnostics console: a viewer over the selected env's erun trace log and the
// in-app UI (Redux) trace. It replaced the old raw-PTY mirror that filled with
// ANSI gibberish whenever a TUI ran in the active session.
//
// The suite cannot stage a populated trace.log without depending on which
// commands ran on this machine, so the erun-trace content path is covered by Go
// tests instead — TestLoadEnvTrace* in erun-ui/env_trace_handlers_test.go and
// TestActivateEnvTrace* in erun-common/env_trace_test.go. Here we lock the
// rendered shell: tab structure, empty states, the UI-trace record/clear cycle,
// and the no-raw-ANSI invariant.
test.describe('diagnostics console', () => {
  test.beforeEach(async ({ app }) => {
    if (!(await app.debugPanel.isOpen())) {
      await app.debugPanel.toggle();
      await expect(app.debugPanel.resizeHandle()).toBeVisible();
    }
  });

  test.afterEach(async ({ app }) => {
    if (await app.debugPanel.isOpen()) {
      await app.debugPanel.toggle();
    }
  });

  test('renders the erun trace and UI trace tabs with erun trace active', async ({ app }) => {
    await expect(app.debugPanel.tab('erun trace')).toBeVisible();
    await expect(app.debugPanel.tab('UI trace')).toBeVisible();
    await expect(app.debugPanel.tab('erun trace')).toHaveAttribute('aria-selected', 'true');
    await expect(app.debugPanel.erunTracePane()).toBeVisible();
    await expect(app.debugPanel.refreshButton()).toBeVisible();
    await expect(app.debugPanel.copyButton()).toBeVisible();
    await expect(app.debugPanel.copyReportButton()).toBeVisible();
    await expect(app.debugPanel.clearButton()).toBeVisible();
    await expect(app.debugPanel.clearButton()).toBeDisabled();
  });

  test('pane actions live outside the scroll regions and the report button spans tabs', async ({
    app,
  }) => {
    // The toolbars used to render inside the scroll region, so stick-to-bottom
    // pushed Copy/Refresh out of view once always-on capture filled the pane;
    // they must now sit outside it.
    await expect(app.debugPanel.erunTracePane().getByRole('button')).toHaveCount(0);
    await expect(app.debugPanel.refreshButton()).toBeVisible();

    await app.debugPanel.selectTab('UI trace');
    await expect(app.debugPanel.uiTracePane().getByRole('button')).toHaveCount(0);
    await expect(app.debugPanel.clearButton()).toBeVisible();
    await expect(app.debugPanel.copyReportButton()).toBeVisible();
  });

  test('UI trace records dispatched actions and Clear empties it', async ({ app }) => {
    await app.debugPanel.selectTab('UI trace');
    await expect(app.debugPanel.uiTracePane()).toBeVisible();

    await app.titlebar.toggleSidebar();
    await app.titlebar.toggleSidebar();

    // A sidebar toggle mutates the layout slice, and each entry renders the
    // changed slice names — so the recorded text must contain 'layout'.
    await expect
      .poll(async () => (await app.debugPanel.uiTracePane().textContent()) ?? '')
      .toMatch(/layout/);

    await app.debugPanel.clearButton().click();
    await expect(app.debugPanel.uiTracePane()).toContainText('No UI activity recorded yet.');
  });

  test('panel surfaces contain no raw ANSI escape sequences', async ({ app }) => {
    // The old panel mirrored raw PTY bytes, so an active TUI turned it into
    // escape-code gibberish. The console no longer reads PTY data, so every
    // pane must render plain text.
    const erunText = (await app.debugPanel.erunTracePane().textContent()) ?? '';
    expect(erunText).not.toMatch(/\x1b/);

    await app.debugPanel.selectTab('UI trace');
    const uiText = (await app.debugPanel.uiTracePane().textContent()) ?? '';
    expect(uiText).not.toMatch(/\x1b/);
  });
});
