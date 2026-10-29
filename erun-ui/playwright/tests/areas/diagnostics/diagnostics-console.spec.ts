import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';

// The pane refills from the ~1s idle poll within one 500ms tick of Clear, so the
// observer must be running before the click or it can miss the empty state.
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

// A populated trace.log can't be staged deterministically; its content path is covered by Go tests.
test.describe('diagnostics console', () => {
  test.beforeEach(async ({ app }) => {
    if (!(await app.debugPanel.isOpen())) {
      await app.debugPanel.toggle();
      await app.debugPanel.waitForOpen();
    }
  });

  test.afterEach(async ({ app }) => {
    if (await app.debugPanel.isOpen()) {
      await app.debugPanel.toggle();
      // Converge on the close so a slow restore can't leak an open panel into the next test.
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
    // Toolbars must sit outside the scroll region so stick-to-bottom can't push them out of view.
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

    await expect
      .poll(async () => (await app.debugPanel.uiTracePane().textContent()) ?? '', withTestBudget())
      .toMatch(/layout/);

    await clearUITraceAndWaitForReset(app);
  });

  test('panel surfaces contain no raw ANSI escape sequences', async ({ app }) => {
    const erunText = (await app.debugPanel.erunTracePane().textContent()) ?? '';
    expect(erunText).not.toMatch(/\x1b/);

    await app.debugPanel.selectTab('UI trace');
    const uiText = (await app.debugPanel.uiTracePane().textContent()) ?? '';
    expect(uiText).not.toMatch(/\x1b/);
  });
});
