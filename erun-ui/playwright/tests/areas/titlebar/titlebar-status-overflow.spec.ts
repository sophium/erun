import { expect, test } from '../../../fixtures/erunApp.js';

// Message-centre rows always show full text; only the app-status pill keeps
// the truncate-then-popover escalation. Copy-on-error is covered in app_test.go.

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

    // Compare full length to catch any truncation.
    const text = await row.textContent();
    expect((text ?? '').length).toBeGreaterThanOrEqual(LONG_MESSAGE.length);
  });

  // Below the threshold, status stays a tooltip.
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
    await expect(page.getByTestId('titlebar-status-message')).toHaveCount(0);
  });

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
