import assert from 'node:assert/strict';

import type { SerializeAddon } from '@xterm/addon-serialize';
import type { Terminal } from '@xterm/xterm';
import { test } from 'vitest';

import type { store as appStore } from './store';
import type { TerminalController } from './TerminalController';

// The frontend bundle targets a browser, and the node test environment is not
// one. Two globals it assumes are handed over by hand here, the same way the
// other frontend tests stub the Wails bridge by hand:
//
//   - the xterm addons ship UMD bundles that read `self` at module scope, so it
//     has to exist before anything importing them is evaluated (which is why
//     every import below that reaches `@xterm/addon-fit` is dynamic);
//   - activation reaches the Wails bridge and the reattach poller reaches the
//     browser timers through `window`.
interface BrowserGlobalsWindow {
  go: { main: { App: Record<string, (...args: never[]) => Promise<undefined>> } };
  setTimeout: typeof globalThis.setTimeout;
  clearTimeout: typeof globalThis.clearTimeout;
  setInterval: (handler: () => void, timeout: number) => number;
  clearInterval: (id: number) => void;
  innerWidth: number;
}

// The pane-geometry push every activation ends with (resizeActiveSession ->
// runTerminalResize -> applyTerminalLayoutVars) writes the runtime CSS variables
// on the document root. There is no layout in this environment, so the root is
// the one property write that path needs.
interface BrowserGlobalsDocument {
  documentElement: { style: { setProperty: (name: string, value: string) => void } };
}

(globalThis as unknown as { self: unknown }).self = globalThis;
(globalThis as unknown as { document: BrowserGlobalsDocument }).document = {
  documentElement: { style: { setProperty: () => undefined } },
};
(globalThis as unknown as { window: BrowserGlobalsWindow }).window = {
  go: {
    main: {
      App: {
        RepaintSession: () => Promise.resolve(undefined),
        ResizeSession: () => Promise.resolve(undefined),
      },
    },
  },
  setTimeout: globalThis.setTimeout,
  clearTimeout: globalThis.clearTimeout,
  // The reattach poller arms a 1.3s browser interval on every activation. It is
  // a repaint aid for a blank pane, not part of the switch ordering under test
  // here, and a live handle would outlive the case.
  setInterval: () => 0,
  clearInterval: () => undefined,
  innerWidth: 1440,
};

const OUTGOING = 1;
const INCOMING = 2;
// Two lines, so a snapshot that lost the screen is distinguishable from one
// that lost only its tail.
const OUTGOING_SCREEN = 'alpha one\r\nalpha two\r\n';

interface Driver {
  controller: TerminalController;
  terminal: Terminal;
  serialize: SerializeAddon;
  store: typeof appStore;
}

// xterm parses write() on a macrotask: with an empty queue, WriteBuffer.write
// schedules its drain with setTimeout. So "the pane has caught up" is one turn
// of the macrotask queue -- not a sleep, and nothing here is timed.
async function drainWriteQueue(): Promise<void> {
  await new Promise<void>((resolve) => {
    setTimeout(resolve, 0);
  });
  await new Promise<void>((resolve) => {
    setTimeout(resolve, 0);
  });
}

// A real TerminalController driving a real xterm Terminal through the real
// terminalDisplayMiddleware on the real store: dispatch(setSessionId(...)) in
// the cases below is the same call the thunks make.
async function mountDriver(): Promise<Driver> {
  const { Terminal: Xterm } = await import('@xterm/xterm');
  const { SerializeAddon: Serializer } = await import('@xterm/addon-serialize');
  const { TerminalController: Controller } = await import('./TerminalController');
  const { store } = await import('./store');
  const controller = new Controller();
  const terminal = new Xterm({ cols: 80, rows: 24, scrollback: 1000 });
  const serialize = new Serializer();
  terminal.loadAddon(serialize);
  // switchSession, snapshotSession and resetTerminal all reach xterm through
  // these two, and the controller owns both; a mounted pane is not what this
  // driver is about, so they are placed directly.
  (controller as unknown as { terminal: Terminal }).terminal = terminal;
  (controller as unknown as { serializeAddon: SerializeAddon }).serializeAddon = serialize;
  return { controller, terminal, serialize, store };
}

// handleTerminalOutput does both of these in one step -- append the chunk to
// the session's display buffer, then write it to the pane -- and they are the
// two things switchSession reads: the buffer length is the index the snapshot
// keeps from, and the pane is what the snapshot serializes.
//
// The drain matters: both thunks dispatch from a dialog control, so the pane
// they switch away from is settled -- its last chunk parsed long ago, with
// nothing left in xterm's queue. That is the state a reset in the switch tick
// truncates, and it is the state these cases drive.
async function seedOutgoingScreen(driver: Driver): Promise<void> {
  driver.controller.sessions.appendDisplayBuffer(OUTGOING, OUTGOING_SCREEN);
  driver.terminal.write(OUTGOING_SCREEN);
  await drainWriteQueue();
}

// Brings the store onto OUTGOING the way any tab switch does, then lets that
// switch drain, so the pane is settled and the next dispatch's previous session
// is the one whose screen these cases are about.
async function activateOutgoing(driver: Driver): Promise<void> {
  const { setSessionId } = await import('./slices/terminalSlice');
  driver.store.dispatch(setSessionId(OUTGOING));
  await drainWriteQueue();
}

async function withControllerDriven(
  body: (driver: Driver) => Promise<void>,
  driver: Driver,
): Promise<void> {
  const { thunkExtra } = await import('./thunkExtra');
  thunkExtra.controller = driver.controller;
  try {
    await body(driver);
  } finally {
    thunkExtra.controller = null;
  }
}

// The control: with nothing else touching the pane, the switch captures the
// outgoing session's rendered screen for its switch-back. This is the property
// the fix under test has to keep, and what makes the case below a regression
// rather than a pre-existing gap.
test('a switch captures the outgoing session screen for the switch back', async () => {
  const { setSessionId } = await import('./slices/terminalSlice');
  const driver = await mountDriver();
  await withControllerDriven(async (d) => {
    await activateOutgoing(d);
    await seedOutgoingScreen(d);

    d.store.dispatch(setSessionId(INCOMING));
    await drainWriteQueue();

    assert.match(
      d.controller.sessions.snapshot(OUTGOING) ?? '',
      /alpha one/,
      'the switch away from a session must snapshot the screen it was showing',
    );
  }, driver);
});

// The reproduction. globalConfigCloudThunks (startCloudInitSession) and
// manageDeleteThunks both dispatch setSessionId and then call
// controller.resetTerminal() directly, in the same tick, for the session they
// are moving off. The switch itself is ordered behind the outgoing session's
// pending writes -- that ordering is the fix this branch exists for -- so a
// direct reset in that tick can only either run before that ordering (wiping
// the pane the deferred snapshot is about to serialize, which is what this case
// pins) or be ordered with it. On the pre-fix code it runs before it, the
// snapshot of the outgoing session comes back blank, and the operator's
// scrollback for that session is gone on the next switch back.
test('a direct resetTerminal in the switch tick does not blank the outgoing snapshot', async () => {
  const { setSessionId } = await import('./slices/terminalSlice');
  const driver = await mountDriver();
  await withControllerDriven(async (d) => {
    await activateOutgoing(d);
    await seedOutgoingScreen(d);

    d.store.dispatch(setSessionId(INCOMING));
    // Exactly what globalConfigCloudThunks.ts does on the statement after its
    // setSessionId dispatch, in the same synchronous run of the thunk body.
    d.controller.resetTerminal();
    await drainWriteQueue();

    const captured = d.controller.sessions.snapshot(OUTGOING) ?? '';
    assert.match(
      captured,
      /alpha one/,
      `a resetTerminal in the switch tick truncated the outgoing session's snapshot to ${JSON.stringify(captured)}`,
    );
  }, driver);
});

// manageDeleteThunks is the other direct caller, and it takes the close shape:
// setSessionId(0), no activation to follow. The switch owns that reset too, so
// the outgoing session's snapshot has to survive AND the pane still has to end
// up cleared -- the second half is what a fix that simply swallowed the direct
// reset without the switch performing one would get wrong.
test('a direct resetTerminal in the close tick still clears the pane', async () => {
  const { setSessionId } = await import('./slices/terminalSlice');
  const driver = await mountDriver();
  await withControllerDriven(async (d) => {
    await activateOutgoing(d);
    await seedOutgoingScreen(d);

    d.store.dispatch(setSessionId(0));
    d.controller.resetTerminal();
    await drainWriteQueue();

    assert.match(
      d.controller.sessions.snapshot(OUTGOING) ?? '',
      /alpha one/,
      'the close must still capture the outgoing session screen',
    );
    assert.equal(d.serialize.serialize().trim(), '', 'the closed pane must end up cleared');
  }, driver);
});
