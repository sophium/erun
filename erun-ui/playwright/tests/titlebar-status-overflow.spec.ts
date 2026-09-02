import { expect, test } from '../fixtures/erunApp.js';

// The message centre retired LONG_STATUS_THRESHOLD's truncate-then-popover
// escalation for classified (app-notification) messages: the dialog
// always shows a row's full text, so there is no truncation to escalate out
// of. The mechanism survives only for the terminal/command status pill
// (app-status), which is unaffected by that redesign -- both are covered
// below. The other half of the original fix — always showing the copy button
// on errors — needs terminal-exit sessions the harness does not stage during
// boot, and is covered by the Go-side erun-ui app_test.go suite.

const LONG_MESSAGE =
  'aws ec2 start-instances --instance-ids i-0de894ac09f66d87e: An error occurred ' +
  '(IncorrectInstanceState) when calling the StartInstances operation: The instance ' +
  "'i-0de894ac09f66d87e' is not in a state from which it can be started.";

test.describe('titlebar status overflow', () => {
  test('a long classified error shows its full text in the message centre dialog, untruncated', async ({
    app,
    page,
  }) => {
    expect(LONG_MESSAGE.length).toBeGreaterThan(160);

    await page.evaluate((message) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('app-notification', { kind: 'error', message });
    }, LONG_MESSAGE);

    await app.titlebar.openMessageCenter('error');
    const row = app.titlebar.messageCenterRow('IncorrectInstanceState');
    await expect(row).toBeVisible();
    await expect(row).toContainText('is not in a state from which it can be started');

    // Compare full length, not just substring presence, to catch a regression
    // that reintroduces truncation.
    const text = await row.textContent();
    expect((text ?? '').length).toBeGreaterThanOrEqual(LONG_MESSAGE.length);
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

  // The terminal/command status pill (app-status) keeps the truncate →
  // click-to-expand-popover escalation this whole mechanism originally
  // existed for; only the classified message centre had it retired, not
  // this channel.
  test('a long terminal-status message still escalates to a selectable popover', async ({
    app: _app,
    page,
  }) => {
    await page.evaluate((message) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('app-status', { message, busy: false });
    }, LONG_MESSAGE);

    const trigger = page.getByTestId('titlebar-status-message');
    await expect(trigger).toBeVisible();
    await trigger.click();

    const fullText = page.getByTestId('titlebar-status-full-text');
    await expect(fullText).toBeVisible();
    await expect(fullText).toContainText('IncorrectInstanceState');
  });
});
