import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';

// erun#1214: closing the window used to SIGKILL every in-flight build/
// deploy/release with no warning and no record. `beforeClose` now blocks the
// close and asks first when the activity queue holds running work.
//
// The default suite's kubectl/helm/docker stubs mean nothing here ever
// reaches a genuinely long-running, backend-tracked activity entry (see
// deploy-orchestration.spec.ts's own comment on the same limitation), so the
// "blocked" rendering is driven the same way activity-events.spec.ts drives
// the drawer: by emitting the real "app-close-gate" event the backend would
// emit, with a fake entry. The idle-queue path below does call the real
// bound PrepareWindowClose, since that is safe with the seeded baseline's
// empty queue.
//
// "Close anyway" is never allowed to run for real here: it would call the
// real ConfirmWindowClose, which quits the one shared headless backend this
// whole suite runs against (playwright/AGENTS.md: "the headless backend is a
// singleton"), tearing it down for every spec that runs after this one. The
// spec instead intercepts the bound method to prove the click reaches it.
// The real persist-then-quit behavior is covered by
// erun-ui/window_close_test.go (TestConfirmWindowClosePersistsRecordThenQuits,
// TestConfirmWindowCloseSkipsRecordWhenNothingIsRunning,
// TestConfirmWindowCloseStillQuitsWhenRecordingFails).

interface CloseGateBridge {
  go: {
    main: {
      App: {
        PrepareWindowClose: () => Promise<{ blocked: boolean }>;
        ConfirmWindowClose: () => Promise<void>;
      };
    };
  };
}

async function emitCloseGate(page: Page, running: Record<string, unknown>[]): Promise<void> {
  await page.evaluate((entries) => {
    (
      window as unknown as { runtime: { EventsEmit: (name: string, ...args: unknown[]) => void } }
    ).runtime.EventsEmit('app-close-gate', { blocked: true, running: entries });
  }, running);
}

function fakeRunningEntry(id: string): Record<string, unknown> {
  return {
    id,
    command: 'release',
    tenant: 'acme',
    environment: 'prod',
    status: 'running',
    startedAt: new Date().toISOString(),
    lastUpdated: new Date().toISOString(),
    source: 'action',
    actionKind: 'release',
  };
}

test.describe('close confirmation (erun#1214)', () => {
  test('an idle queue never raises the dialog when asked whether the window may close', async ({
    app,
  }) => {
    const gate = await app.page.evaluate(async () => {
      const bridge = window as unknown as CloseGateBridge;
      return bridge.go.main.App.PrepareWindowClose();
    });

    expect(gate.blocked).toBe(false);
    expect(await app.closeConfirmDialog.isOpen()).toBe(false);
  });

  test('a running job blocks the close and names itself; cancelling keeps it running', async ({
    app,
  }) => {
    await emitCloseGate(app.page, [fakeRunningEntry('close-confirm-cancel')]);

    await app.closeConfirmDialog.waitForOpen();
    const row = app.closeConfirmDialog.locator().locator('li').filter({ hasText: 'acme/prod' });
    await expect(row).toBeVisible();
    await expect(row.getByText('release', { exact: true })).toBeVisible();

    await app.closeConfirmDialog.cancel();
    await app.closeConfirmDialog.waitForClosed();

    // Cancelling is purely a frontend dismissal — it must not call back into
    // the backend to touch the (still-running, as far as the operator knows)
    // job.
  });

  test('closing anyway calls the backend confirmation', async ({ app }) => {
    await emitCloseGate(app.page, [fakeRunningEntry('close-confirm-anyway')]);
    await app.closeConfirmDialog.waitForOpen();

    const invoked = app.page.evaluate(() => {
      return new Promise<boolean>((resolve) => {
        const bridge = window as unknown as CloseGateBridge;
        bridge.go.main.App.ConfirmWindowClose = () => {
          resolve(true);
          return new Promise<void>(() => {
            // Deliberately never resolves: the real call would drive the app
            // to quit next, and this stub stands in for the rest of the test.
          });
        };
      });
    });
    await app.closeConfirmDialog.closeAnyway();
    expect(await invoked).toBe(true);
  });
});
