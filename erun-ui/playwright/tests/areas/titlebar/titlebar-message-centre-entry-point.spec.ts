import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

// The "Message history" fallback renders only while no class is unread, so
// each titlebar shape is driven deterministically here, one per case.

function emit(
  page: import('@playwright/test').Page,
  payload: Record<string, unknown>,
): Promise<void> {
  return page.evaluate((notification) => {
    const runtime = (
      window as unknown as {
        runtime: { EventsEmit: (n: string, ...a: unknown[]) => void };
      }
    ).runtime;
    runtime.EventsEmit('app-notification', notification);
  }, payload);
}

test.describe('message centre entry point', () => {
  // A stray unread class from another spec suppresses the fallback; the centre
  // stays reachable through that class's icon.
  test('stays reachable through another class icon while that class is unread', async ({
    app,
    page,
  }) => {
    const otherClass = 'Left unread by another spec: the edge is not answering right now.';
    await emit(page, { kind: 'error', message: otherClass });
    await app.titlebar.messageCenterIcon('error').waitFor({ state: 'visible' });

    const message = 'Info entry that must survive into history.';
    await emit(page, { kind: 'info', message });
    await app.titlebar.messageCenterIcon('info').waitFor({ state: 'visible' });

    await expect(app.titlebar.messageCenterIcon('info')).toHaveCount(0, withTestBudget());

    await expect(app.titlebar.messageCenterHistoryButton()).toHaveCount(0, withTestBudget());

    await app.titlebar.messageCenterEntryPoint().waitFor({ state: 'visible' });
    await app.titlebar.openMessageCenterFromTitlebar();
    await app.titlebar.messageCenterRow(message).waitFor({ state: 'visible' });
  });

  // "Mark all read" clears any class an earlier spec left unread.
  test('stays reachable through the history fallback once nothing is unread', async ({
    app,
    page,
  }) => {
    const message = 'Info entry with a quiet titlebar around it.';
    await emit(page, { kind: 'info', message });
    await app.titlebar.messageCenterIcon('info').waitFor({ state: 'visible' });

    await app.titlebar.openMessageCenter('info');
    await app.titlebar.messageCenterMarkAllReadButton().click();
    // The titlebar is aria-hidden while the modal is open.
    await app.titlebar.closeMessageCenter();

    await expect(app.titlebar.messageCenterHistoryButton()).toHaveCount(1, withTestBudget());
    await app.titlebar.openMessageCenterFromTitlebar();
    await app.titlebar.messageCenterRow(message).waitFor({ state: 'visible' });
  });
});
