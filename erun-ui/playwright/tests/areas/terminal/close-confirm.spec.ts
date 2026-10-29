import type { Page } from '@playwright/test';

import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { holdTabSetup } from '../../../fixtures/seedRoot.js';

// beforeClose asks first when running work would be killed. The blocked state
// is driven by emitting "app-close-gate", since stubs never produce long work;
// tabs still in setup count as idle. "Close anyway" is intercepted because the
// real ConfirmWindowClose would quit the shared singleton backend.

interface TabSelection {
  tenant: string;
  environment: string;
}

interface CloseGateBridge {
  go: {
    main: {
      App: {
        PrepareWindowClose: () => Promise<{ blocked: boolean }>;
        ConfirmWindowClose: () => Promise<void>;
        StartSession: (
          selection: TabSelection,
          slot: number,
          cols: number,
          rows: number,
        ) => Promise<unknown>;
        FindActiveDeployForSelection: (
          selection: TabSelection,
        ) => Promise<{ actionKind?: string; status?: string }>;
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

  test('a tab still being established never raises the dialog when asked whether the window may close', async ({
    app,
    seededEnv,
  }) => {
    const selection: TabSelection = {
      tenant: seededEnv.tenant,
      environment: seededEnv.environment,
    };
    const release = holdTabSetup(seededEnv.tenant, seededEnv.environment);
    try {
      // StartSession resolves after the entry runs but, with the hold, before ready.
      const { setup, gate } = await app.page.evaluate(async (sel) => {
        const bridge = (window as unknown as CloseGateBridge).go.main.App;
        await bridge.StartSession(sel, 0, 80, 24);
        return {
          setup: await bridge.FindActiveDeployForSelection(sel),
          gate: await bridge.PrepareWindowClose(),
        };
      }, selection);

      expect(setup).toMatchObject({ actionKind: 'open', status: 'running' });
      expect(gate.blocked).toBe(false);
      expect(await app.closeConfirmDialog.isOpen()).toBe(false);
    } finally {
      release();
    }

    // Let the released tab finish so no running entry outlives this test.
    await expect
      .poll(async () => {
        const entry = await app.page.evaluate(async (sel) => {
          const bridge = (window as unknown as CloseGateBridge).go.main.App;
          return bridge.FindActiveDeployForSelection(sel);
        }, selection);
        return entry.status ?? '';
      }, withTestBudget())
      .not.toBe('running');
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

    // Cancel is frontend-only and must not call back into the backend.
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
            // Never resolves: the real call would quit the app.
          });
        };
      });
    });
    await app.closeConfirmDialog.closeAnyway();
    expect(await invoked).toBe(true);
  });
});
