import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';

// xterm parses writes on a macrotask and reset() leaves the queue alone, so a
// write queued just before a switch would render into the incoming session's
// pane and be lost from the outgoing one. The overlap is built in one task by
// calling the app's own terminal-output listener and clicking in the same
// evaluate; xterm's FIFO queue makes the incoming marker a "rendered" fence.

const OUTGOING_MARKER = 'erun-switch-write-outgoing';
const INCOMING_MARKER = 'erun-switch-write-incoming';
const STEP_BUDGET_MS = 20_000;
const SCENARIO_MARGIN_MS = 30_000;
// Steps: tab spawn, incoming fence, outgoing marker survival, teardown.
const SCENARIO_BOUNDS = 4;
const SCENARIO_BUDGET_MS = SCENARIO_BOUNDS * STEP_BUDGET_MS + SCENARIO_MARGIN_MS;

interface CaptureWindow extends Window {
  __erunTerminalOutputListener?: (payload: { sessionId: number; data: string }) => void;
}

// The listener registers at page load, so only intercepting the runtime
// assignment can observe it.
async function captureTerminalOutputListener(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const install = (runtime: {
      EventsOnMultiple: (name: string, ...rest: unknown[]) => unknown;
    }) => {
      const register = runtime.EventsOnMultiple.bind(runtime);
      runtime.EventsOnMultiple = (name: string, ...rest: unknown[]) => {
        if (name === 'terminal-output') {
          (window as CaptureWindow).__erunTerminalOutputListener =
            rest[0] as CaptureWindow['__erunTerminalOutputListener'];
        }
        return register(name, ...rest);
      };
    };
    const existing = Object.getOwnPropertyDescriptor(window, 'runtime');
    if (existing?.value) {
      install(existing.value as Parameters<typeof install>[0]);
      return;
    }
    // Wrap the shim's plain assignment, then replace this accessor.
    Object.defineProperty(window, 'runtime', {
      configurable: true,
      get: () => undefined,
      set: (runtime: Parameters<typeof install>[0]) => {
        install(runtime);
        Object.defineProperty(window, 'runtime', {
          configurable: true,
          writable: true,
          value: runtime,
        });
      },
    });
  });
}

// emitThenSwitch queues the outgoing write and clicks the tab in one task.
async function emitThenSwitch(
  app: AppShell,
  { sessionId, data, tabLabel }: { sessionId: number; data: string; tabLabel: string },
): Promise<void> {
  const tab = await app.tabStrip.tab(tabLabel).elementHandle();
  if (tab === null) {
    throw new Error(`the ${tabLabel} tab is not rendered`);
  }
  await app.page.evaluate(
    ({ handle, payloadSessionId, payloadData }) => {
      const listener = (window as CaptureWindow).__erunTerminalOutputListener;
      if (!listener) {
        throw new Error('the app registered no terminal-output listener to drive');
      }
      listener({ sessionId: payloadSessionId, data: payloadData });
      (handle as unknown as HTMLElement).click();
    },
    { handle: tab, payloadSessionId: sessionId, payloadData: data },
  );
}

test.describe('terminal switch write ownership', () => {
  test('output emitted for the outgoing session does not render in the session switched to', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(SCENARIO_BUDGET_MS);
    const { tenant, environment } = seededEnv;

    // Re-open so the init script runs before the listener registers.
    await captureTerminalOutputListener(page);
    await app.open();
    await app.sidebar.openEnvironment(tenant, environment);
    await app.tabStrip.waitForTab('Local');

    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await expect
      .poll(() => extraTabs.count(), { timeout: STEP_BUDGET_MS })
      .toBeGreaterThan(initialExtraCount);
    const outgoingSessionId = await app.terminalPane.selectedSessionId();
    expect(outgoingSessionId).toBeGreaterThan(0);

    await emitThenSwitch(app, {
      sessionId: outgoingSessionId,
      data: btoa(`${OUTGOING_MARKER}\r\n`),
      tabLabel: 'Local',
    });

    const incomingSessionId = await app.terminalPane.selectedSessionId();
    expect(incomingSessionId).toBeGreaterThan(0);
    expect(incomingSessionId).not.toBe(outgoingSessionId);

    // Fence: once this renders, every earlier write has been parsed.
    await app.terminalPane.emitOutput(incomingSessionId, `${INCOMING_MARKER}\r\n`);
    await expect(app.terminalPane.rows()).toContainText(INCOMING_MARKER, {
      timeout: STEP_BUDGET_MS,
    });

    const incomingPaneText = await app.terminalPane.rows().innerText();

    expect(incomingPaneText).not.toContain(OUTGOING_MARKER);

    // The line must also survive on the session that produced it.
    await extraTabs.last().click();
    await expect(app.terminalPane.rows()).toContainText(OUTGOING_MARKER, {
      timeout: STEP_BUDGET_MS,
    });

    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect(extraTabs).toHaveCount(initialExtraCount, { timeout: STEP_BUDGET_MS });
  });
});
