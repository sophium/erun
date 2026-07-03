import { expect, test } from '../fixtures/erunApp.js';

// The headless harness reaches only half of this fix: a long notification
// escalates from a truncating hover tooltip to a click popover with selectable,
// scrollable content, observable here. The other half — always showing the copy
// button on errors — needs terminal-exit sessions the harness does not stage
// during boot, and is covered by the Go-side erun-ui app_test.go suite.

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

    // Use an error notification, not app-status: a background env's own
    // terminal-status updates would clobber an app-status message mid-assertion,
    // whereas an error notification takes precedence and never auto-dismisses, so
    // it stays put while we open the popover. The escalation renders identically
    // for either source.
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

    // Match by testid, not text: the collapsed trigger shows truncated text, so a
    // text query would race the truncated-vs-full rendering.
    const trigger = page.getByTestId('titlebar-status-message');
    await expect(trigger).toBeVisible();

    await emitLong();
    await trigger.click();

    const fullText = page.getByTestId('titlebar-status-full-text');
    await expect(fullText).toBeVisible();
    await expect(fullText).toContainText('IncorrectInstanceState');
    await expect(fullText).toContainText('is not in a state from which it can be started');

    // Compare full length, not just substring presence, to catch a regression
    // that re-introduces truncation.
    const text = await fullText.textContent();
    expect(text?.trim().length ?? 0).toBeGreaterThanOrEqual(LONG_MESSAGE.length);
  });

  // Guard the short-message path: below the threshold, status stays a hover
  // tooltip and does not escalate to the popover.
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
