import { expect, test } from '../../../fixtures/erunApp.js';

// The message centre's entry point is a property of the whole titlebar, not of
// the entry a spec just raised: Titlebar.MessageCenter.tsx renders the
// "Message history" fallback only while every class icon has nothing unread
// (`visibleKinds.length === 0 && historyCount > 0`), and an unread class's own
// icon otherwise. So a spec that clears its own entry and then pins the
// fallback is asserting a quiet titlebar it cannot enforce -- any other class
// left unread suppresses it. That is not a load-dependent race in the spec's
// own flow: it is a state the spec can reach in one page, which is what these
// two cases do, one titlebar shape each. The condition that used to red a
// full-suite worker was exactly the first case's titlebar (a stray
// backend-originated notice leaving one class unread while the spec's own
// entry had cleared); driving it here makes the reproduction independent of
// worker lifetime, of which spec ran before, and of machine load.

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
  // The reproduction. A class this spec did not raise is unread -- the state a
  // stray notice from another spec in the same worker leaves behind -- while
  // this spec's own entry clears into history. Under the old assertion the
  // fallback the spec then looked for is not there: the component suppresses
  // it while any class is unread, and the centre is reachable only through
  // that other class's icon.
  test('stays reachable through another class icon while that class is unread', async ({
    app,
    page,
  }) => {
    const otherClass = 'Left unread by another spec: the edge is not answering right now.';
    await emit(page, { kind: 'error', message: otherClass });
    await expect(app.titlebar.messageCenterIcon('error')).toBeVisible();

    const message = 'Info entry that must survive into history.';
    await emit(page, { kind: 'info', message });
    await expect(app.titlebar.messageCenterIcon('info')).toBeVisible();

    // The info entry auto-dismisses into history, leaving the error unread --
    // the exact titlebar shape the failing gate reached.
    await expect(app.titlebar.messageCenterIcon('info')).toHaveCount(0);

    // The component's documented gating, pinned: while another class is
    // unread the fallback is not offered at all. This is why a spec cannot
    // assert it after clearing only its own entry.
    await expect(app.titlebar.messageCenterHistoryButton()).toHaveCount(0);

    // What does hold: the centre is still reachable, and the dismissed entry
    // is still readable once reached.
    await expect(app.titlebar.messageCenterEntryPoint()).toBeVisible();
    await app.titlebar.openMessageCenterFromTitlebar();
    await expect(app.titlebar.messageCenterRow(message)).toBeVisible();
  });

  // The other titlebar shape, same invariant: with nothing left unread the
  // entry point offered is the fallback. Clearing every class goes through the
  // dialog's own "Mark all read" rather than through this spec's two emits, so
  // the "nothing is unread" precondition is established rather than assumed --
  // it clears any class an earlier spec left unread too.
  test('stays reachable through the history fallback once nothing is unread', async ({
    app,
    page,
  }) => {
    const message = 'Info entry with a quiet titlebar around it.';
    await emit(page, { kind: 'info', message });
    await expect(app.titlebar.messageCenterIcon('info')).toBeVisible();

    await app.titlebar.openMessageCenter('info');
    await app.titlebar.messageCenterMarkAllReadButton().click();
    // The rest of the titlebar is aria-hidden while the modal dialog is open,
    // so it must close before any titlebar-icon locator is trusted.
    await app.titlebar.closeMessageCenter();

    await expect(app.titlebar.messageCenterHistoryButton()).toBeVisible();
    await app.titlebar.openMessageCenterFromTitlebar();
    await expect(app.titlebar.messageCenterRow(message)).toBeVisible();
  });
});
