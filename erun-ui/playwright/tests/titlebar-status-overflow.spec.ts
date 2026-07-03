import { expect, test } from '../fixtures/erunApp.js';

// titlebar-status-overflow covers the long-error escalation in
// Titlebar.Status.tsx. Before the fix, errors that exceeded the pill
// width truncated with `…` and offered only a hover tooltip — the
// underlying <button> used `cursor-default` so the text was not
// selectable, and `StatusCopyAction` was gated on a non-empty
// terminalCopyOutput (AWS API errors arrive without one). The result
// was that the user could read the full text via hover but could not
// copy it to paste into a bug report.
//
// The fix has two halves:
//   1. Messages whose combined `message + detail` length exceeds
//      LONG_STATUS_THRESHOLD (160 chars) render the trigger as a click
//      Popover with selectable, scrollable content instead of a hover
//      Tooltip.
//   2. showTerminalFailure in notificationThunks.ts defaults an empty
//      copyOutput to message + detail so the copy button is always
//      present on errors.
//
// (1) is reachable from the headless harness by emitting a long
// `app-status` event — which flows through handleAppStatus → setTerminal
// Message and renders through the same StatusMessage component used by
// errors. The popover trigger and selectable content are observable.
//
// (2) is reachable in principle by emitting a `terminal-exit` event for
// a tracked session, but the headless harness does not stage real
// open-selection sessions during boot. Lock the observable invariant
// reachable here (popover renders, full text is in selectable content)
// and rely on the integration of showTerminalFailure + StatusCopyAction
// for the copy-button half — covered by the Go-side erun-ui app_test.go
// suite when terminal-exit is exercised end-to-end.

const LONG_MESSAGE =
  'aws ec2 start-instances --instance-ids i-0de894ac09f66d87e: An error occurred ' +
  '(IncorrectInstanceState) when calling the StartInstances operation: The instance ' +
  "'i-0de894ac09f66d87e' is not in a state from which it can be started.";

test.describe('titlebar status overflow', () => {
  test('long status message renders inside a popover with selectable text', async ({
    app: _app,
    page,
  }) => {
    expect(LONG_MESSAGE.length).toBeGreaterThan(160);

    // Emit the long message as an error notification rather than an app-status
    // (terminalStatus): on a populated ~/.erun an active/reconnecting env pushes
    // its own terminal-status updates that would clobber a long app-status mid-
    // assertion. A notification takes precedence in computeTitlebarStatus and an
    // error notification does not auto-dismiss, so it stays put while we open the
    // popover. The long-message escalation (the actual surface under test) renders
    // identically for either source.
    const emitLong = () =>
      page.evaluate((message) => {
        const runtime = (
          window as unknown as {
            runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
          }
        ).runtime;
        runtime.EventsEmit('app-notification', { kind: 'error', message });
      }, LONG_MESSAGE);

    await emitLong();

    // The popover trigger is a <button> bearing the truncated message.
    // Identified by the data-testid we set on the trigger so the test
    // does not race against the truncated-vs-full text shown in the
    // collapsed state.
    const trigger = page.getByTestId('titlebar-status-message');
    await expect(trigger).toBeVisible();

    await emitLong();
    await trigger.click();

    // PopoverContent renders the full text with whitespace-pre-wrap and
    // select-text so the user can highlight and copy. data-testid pinned
    // for stable lookup.
    const fullText = page.getByTestId('titlebar-status-full-text');
    await expect(fullText).toBeVisible();
    await expect(fullText).toContainText('IncorrectInstanceState');
    await expect(fullText).toContainText('is not in a state from which it can be started');

    // The displayed text must include the entire message — not a
    // truncated suffix. Compare exact length to catch a regression
    // that re-introduces clipping.
    const text = await fullText.textContent();
    expect(text?.trim().length ?? 0).toBeGreaterThanOrEqual(LONG_MESSAGE.length);
  });

  // `app.activityDrawer` uses Tooltip; verify short messages
  // keep the existing tooltip path so we have not regressed the short-
  // message UI. Visibility of system status (Nielsen #1) for short
  // notifications already worked before this change.
  test('short status message keeps tooltip behaviour', async ({ app: _app, page }) => {
    const SHORT = 'Started cloud environment foo-bar.';
    await page.evaluate((message) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('app-status', { message, busy: false });
    }, SHORT);
    await expect(page.getByText(SHORT, { exact: false })).toBeVisible();
    // The popover trigger testid only renders for long messages.
    await expect(page.getByTestId('titlebar-status-message')).toHaveCount(0);
  });
});
