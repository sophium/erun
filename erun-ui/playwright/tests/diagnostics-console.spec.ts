import { test, expect } from '../fixtures/erunApp.js';

// Diagnostics console (issue #466): the bottom panel is a pure viewer with
// two copyable surfaces — the selected env's persistent erun trace log and
// the in-app UI (Redux action) trace. It replaced the old raw-PTY mirror
// that filled with ANSI gibberish whenever a TUI ran in the active session.
//
// Harness limits: the suite cannot stage a populated trace.log for a known
// env (capture is always-on per #508, but the headless run must not depend
// on which commands ran on this machine). The erun-trace content path is
// covered by Go tests instead — TestLoadEnvTrace* in
// erun-ui/env_trace_handlers_test.go (host + pod reads, reachability gate)
// and TestActivateEnvTrace* in erun-common/env_trace_test.go (what gets
// written). Here we lock the rendered shell: tab structure, empty states,
// the UI-trace record/clear cycle, and the no-raw-ANSI invariant.
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
    // The pane always offers Refresh; Copy is present (enabled only when
    // there is content to copy); Copy report sits in the header, available
    // from either tab.
    await expect(app.debugPanel.refreshButton()).toBeVisible();
    await expect(app.debugPanel.copyButton()).toBeVisible();
    await expect(app.debugPanel.copyReportButton()).toBeVisible();
  });

  test('pane actions live outside the scroll regions and the report button spans tabs', async ({
    app,
  }) => {
    // #514: the toolbars used to render inside the scrollable region, so
    // stick-to-bottom pushed Copy/Refresh out the top as soon as always-on
    // capture filled the pane. Lock the structure: the scroll regions
    // contain no buttons at all — the actions are pinned rows above them.
    await expect(app.debugPanel.erunTracePane().getByRole('button')).toHaveCount(0);
    await expect(app.debugPanel.refreshButton()).toBeVisible();

    await app.debugPanel.selectTab('UI trace');
    await expect(app.debugPanel.uiTracePane().getByRole('button')).toHaveCount(0);
    await expect(app.debugPanel.clearButton()).toBeVisible();
    // The header's Copy report stays available regardless of active tab.
    await expect(app.debugPanel.copyReportButton()).toBeVisible();
  });

  test('UI trace records dispatched actions and Clear empties it', async ({ app }) => {
    await app.debugPanel.selectTab('UI trace');
    await expect(app.debugPanel.uiTracePane()).toBeVisible();

    // Dispatch some Redux activity the recorder must pick up.
    await app.titlebar.toggleSidebar();
    await app.titlebar.toggleSidebar();

    // Entries render as `<ISO timestamp>  <action type>  →  <changed slices>`;
    // the sidebar toggle mutates the layout slice.
    await expect
      .poll(async () => (await app.debugPanel.uiTracePane().textContent()) ?? '')
      .toMatch(/layout/);

    await app.debugPanel.clearButton().click();
    await expect(app.debugPanel.uiTracePane()).toContainText('No UI activity recorded yet.');
  });

  test('panel surfaces contain no raw ANSI escape sequences', async ({ app }) => {
    // The regression that motivated #466: the old panel mirrored raw PTY
    // bytes, so an active TUI turned it into escape-code gibberish. The
    // console no longer reads PTY data at all; whatever each pane renders
    // (trace content or an empty-state reason) must be plain text.
    const erunText = (await app.debugPanel.erunTracePane().textContent()) ?? '';
    expect(erunText).not.toMatch(/\x1b/);

    await app.debugPanel.selectTab('UI trace');
    const uiText = (await app.debugPanel.uiTracePane().textContent()) ?? '';
    expect(uiText).not.toMatch(/\x1b/);
  });
});
