import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';

// clearUITrace observes the reset the UI trace's Clear produces.
//
// The pane does not rest empty. The UI trace records every action the app
// dispatches — including the idle-status poll, which re-arms about once a
// second — and the pane folds that buffer in on its own 500ms tick. The
// "No UI activity recorded yet." copy is therefore real for about one tick, and
// then the app's own traffic is back in it, with nothing having touched the
// pane in between.
//
// That makes the observer's *creation time* the race. A click followed by an
// assertion creates the observer in the gap between the two, and on a loaded
// machine the click's own round trip can outlast the window: the observer then
// starts against a pane that has already refilled and waits out the whole test
// budget for a state that was real and is now gone. That is the failure this
// case records — a 30s red whose received text begins with the idle poll's
// entries rather than with the clear.
//
// So install the observer first and make the click the second step.
// page.waitForFunction polls from inside the page (the idiom the smoke suite
// and tests/areas/shell/layout.spec.ts use for a DOM value with no locator to
// wait on), so it is already running when the Clear is dispatched and cannot be
// locked out by that round trip. It carries no explicit timeout, so it
// converges against the budget this test declared rather than expect's 10s
// default.
async function clearUITraceAndWaitForReset(app: AppShell): Promise<void> {
  const paneHandle = await app.debugPanel.uiTracePane().elementHandle();
  if (paneHandle === null) {
    throw new Error('UI trace output pane is not rendered');
  }
  const resetSeen = app.page.waitForFunction(
    (pane) => (pane.textContent ?? '').includes('No UI activity recorded yet.'),
    paneHandle,
  );
  await app.debugPanel.clearButton().click();
  await resetSeen;
}

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
      // Converge through the panel's own helper rather than expect's fixed
      // budget, then on the tab the test needs.
      await app.debugPanel.waitForOpen();
    }
  });

  test.afterEach(async ({ app }) => {
    if (await app.debugPanel.isOpen()) {
      await app.debugPanel.toggle();
      // Converge on the close so a slow restore can't leak an open panel into
      // the next test in this worker.
      await app.debugPanel.waitForClosed();
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
    // withTestBudget, not expect's 10s default: the entry travels the app's
    // own dispatch -> record -> render path, which this test declared 30s for.
    await expect
      .poll(async () => (await app.debugPanel.uiTracePane().textContent()) ?? '', withTestBudget())
      .toMatch(/layout/);

    await clearUITraceAndWaitForReset(app);
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
