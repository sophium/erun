import type { Page } from '@playwright/test';

import { expect, test } from '../../../fixtures/erunApp.js';
import type { AppShell } from '../../../pages/index.js';

// One pane, two writers. A session's live output reaches the shared xterm
// through TerminalController.handleTerminalOutput, which writes only for the
// session the store names; the switch path -- snapshotSession -> resetTerminal
// -> activateSession, driven by terminalDisplayMiddleware -- is the other, and
// it runs at a moment when the outgoing session's last write has not rendered
// yet. xterm's WriteBuffer.write never parses synchronously: with an empty
// queue it pushes the chunk and schedules the parse on a macrotask
// (`setTimeout(() => this._innerWrite())`). reset()/clear() reset the buffer
// synchronously and leave that queue alone -- xterm's own note on
// Terminal.reset is that it "does not clear input buffers and does not reset
// the parser, thus the terminal will continue to apply pending input data" --
// so bytes written a moment before the switch parse *after* the reset and
// after the incoming session's screen has been restored, and paint the
// outgoing session's output into the pane now showing the incoming one.
//
// It is not only a wrong render. The snapshot taken on the way out runs before
// those bytes parsed, so it does not carry them, and captureSnapshot clears the
// display buffer in the same step -- so the line is gone from the session that
// actually produced it and reappears, if at all, only on the session that did
// not.
//
// No budget closes this: waiting before the reset delays the switch, and waiting
// after it cannot un-render bytes already parsed into the wrong screen. The
// ordering is the fix, so the ordering is what this spec drives.
//
// The overlap is constructed rather than waited for, and no clock is involved.
// The headless shim routes every app listener through window.runtime
// .EventsOnMultiple (wailsjs/runtime/runtime.js's EventsOn delegates to it), so
// intercepting it at document start hands this spec the app's own
// terminal-output listener. Calling that listener and then clicking the
// incoming tab inside one page.evaluate puts the outgoing session's write and
// the switch in the same task -- the write is queued (its parse is a macrotask
// and has not run), and the switch is not.
//
// The two assertions after it are ordered against xterm's queue, which is FIFO:
// a marker written for the incoming session is queued behind everything already
// pending, so converging on it is the deterministic "everything before this has
// rendered" signal. That is what bounds the absence check -- no sleep stands in
// for it, and the positive checks converge on their own text.

const OUTGOING_MARKER = 'erun-switch-write-outgoing';
const INCOMING_MARKER = 'erun-switch-write-incoming';
const STEP_BUDGET_MS = 20_000;
const SCENARIO_MARGIN_MS = 30_000;
// The convergence steps below: the extra tab's spawn, the incoming session's
// fence marker, the outgoing marker's survival, and the tab-count teardown.
const SCENARIO_BOUNDS = 4;
const SCENARIO_BUDGET_MS = SCENARIO_BOUNDS * STEP_BUDGET_MS + SCENARIO_MARGIN_MS;

interface CaptureWindow extends Window {
  __erunTerminalOutputListener?: (payload: { sessionId: number; data: string }) => void;
}

// Taps the app's own terminal-output listener, before any app script runs. The
// app subscribes from TerminalController.mount, which App runs on its first
// effect -- i.e. at page load, before the app fixture returns -- so intercepting
// the runtime assignment is the only point at which the registration can still
// be observed.
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
    // The shim assigns window.runtime as a plain `window.runtime = {...}`; the
    // accessor below wraps it at that assignment and then replaces itself.
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

// emitThenSwitch is the interleaving itself: one synchronous task that hands the
// outgoing session's output to the app's own listener (queueing its xterm write
// on a macrotask) and then clicks the tab to switch to, so the switch is
// dispatched with that write still unparsed.
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

    // The app fixture has already booted the bundle once; re-entering through
    // the fixture's own open() is what puts the init script above in front of
    // it, since the listener this case needs is registered during that boot.
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

    // The defect, in one task: output for the session on screen right now, and
    // the switch away from it, with no macrotask between them -- so xterm has
    // not parsed that output when the switch resets the pane.
    await emitThenSwitch(app, {
      sessionId: outgoingSessionId,
      data: btoa(`${OUTGOING_MARKER}\r\n`),
      tabLabel: 'Local',
    });

    // The pane moved: the store's active session is no longer the one the
    // marker was written for.
    const incomingSessionId = await app.terminalPane.selectedSessionId();
    expect(incomingSessionId).toBeGreaterThan(0);
    expect(incomingSessionId).not.toBe(outgoingSessionId);

    // The fence. This write is issued for the incoming session now, so it is
    // queued behind everything already pending; once it is on screen, every
    // write issued before it has been parsed and rendered.
    await app.terminalPane.emitOutput(incomingSessionId, `${INCOMING_MARKER}\r\n`);
    await expect(app.terminalPane.rows()).toContainText(INCOMING_MARKER, {
      timeout: STEP_BUDGET_MS,
    });

    // One read of the incoming pane, taken once the fence above has landed and
    // before anything below moves the pane again.
    const incomingPaneText = await app.terminalPane.rows().innerText();

    // The reported failure: the outgoing session's bytes, rendered into the
    // pane that is showing the incoming session.
    expect(incomingPaneText).not.toContain(OUTGOING_MARKER);

    // And the half of it that outlives the switch -- the line is not on the
    // session that produced it either, because the snapshot taken on the way
    // out ran before it parsed and captureSnapshot dropped the delta with it.
    // A fix that only cleared the incoming pane would leave this one red.
    await extraTabs.last().click();
    await expect(app.terminalPane.rows()).toContainText(OUTGOING_MARKER, {
      timeout: STEP_BUDGET_MS,
    });

    // Clean up so the spawned terminal does not leak into the singleton
    // backend's session set.
    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect(extraTabs).toHaveCount(initialExtraCount, { timeout: STEP_BUDGET_MS });
  });
});
