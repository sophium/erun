import assert from 'node:assert/strict';

import type { SerializeAddon } from '@xterm/addon-serialize';
import type { Terminal } from '@xterm/xterm';
import { test } from 'vitest';

import type { store as appStore } from './store';
import type { TerminalController } from './TerminalController';

// The xterm addons' UMD bundles read `self` at module scope, so it must exist
// before they load (hence the dynamic imports); activation reaches the Wails
// bridge and timers through `window`.
interface BrowserGlobalsWindow {
  go: { main: { App: Record<string, (...args: never[]) => Promise<undefined>> } };
  setTimeout: typeof globalThis.setTimeout;
  clearTimeout: typeof globalThis.clearTimeout;
  setInterval: (handler: () => void, timeout: number) => number;
  clearInterval: (id: number) => void;
  innerWidth: number;
}

// applyTerminalLayoutVars writes CSS variables on the document root.
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
  // The reattach poller's interval would outlive the case.
  setInterval: () => 0,
  clearInterval: () => undefined,
  innerWidth: 1440,
};

const OUTGOING = 1;
const INCOMING = 2;
// Two lines, so losing the screen differs from losing only its tail.
const OUTGOING_SCREEN = 'alpha one\r\nalpha two\r\n';

interface Driver {
  controller: TerminalController;
  terminal: Terminal;
  serialize: SerializeAddon;
  store: typeof appStore;
}

// Twice: a switch's callback writes its replay behind the first empty write's
// callback, so the second drain lands behind the replay.
async function drainWriteQueue(driver: Driver): Promise<void> {
  for (let pass = 0; pass < 2; pass += 1) {
    await new Promise<void>((resolve) => {
      driver.terminal.write('', () => {
        resolve();
      });
    });
  }
}

async function mountDriver(): Promise<Driver> {
  const { Terminal: Xterm } = await import('@xterm/xterm');
  const { SerializeAddon: Serializer } = await import('@xterm/addon-serialize');
  const { TerminalController: Controller } = await import('./TerminalController');
  const { store } = await import('./store');
  const controller = new Controller();
  const terminal = new Xterm({ cols: 80, rows: 24, scrollback: 1000 });
  const serialize = new Serializer();
  terminal.loadAddon(serialize);
  (controller as unknown as { terminal: Terminal }).terminal = terminal;
  (controller as unknown as { serializeAddon: SerializeAddon }).serializeAddon = serialize;
  return { controller, terminal, serialize, store };
}

// Drained: both thunks dispatch from a dialog, so the outgoing pane is settled.
async function seedOutgoingScreen(driver: Driver): Promise<void> {
  driver.controller.sessions.appendDisplayBuffer(OUTGOING, OUTGOING_SCREEN);
  driver.terminal.write(OUTGOING_SCREEN);
  await drainWriteQueue(driver);
}

async function activateOutgoing(driver: Driver): Promise<void> {
  const { setSessionId } = await import('./slices/terminalSlice');
  driver.store.dispatch(setSessionId(OUTGOING));
  await drainWriteQueue(driver);
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

test('a switch captures the outgoing session screen for the switch back', async () => {
  const { setSessionId } = await import('./slices/terminalSlice');
  const driver = await mountDriver();
  await withControllerDriven(async (d) => {
    await activateOutgoing(d);
    await seedOutgoingScreen(d);

    d.store.dispatch(setSessionId(INCOMING));
    await drainWriteQueue(d);

    assert.match(
      d.controller.sessions.snapshot(OUTGOING) ?? '',
      /alpha one/,
      'the switch away from a session must snapshot the screen it was showing',
    );
  }, driver);
});

// globalConfigCloudThunks and manageDeleteThunks call resetTerminal() in the
// same tick as setSessionId; that reset must not blank the deferred snapshot.
test('a direct resetTerminal in the switch tick does not blank the outgoing snapshot', async () => {
  const { setSessionId } = await import('./slices/terminalSlice');
  const driver = await mountDriver();
  await withControllerDriven(async (d) => {
    await activateOutgoing(d);
    await seedOutgoingScreen(d);

    d.store.dispatch(setSessionId(INCOMING));
    d.controller.resetTerminal();
    await drainWriteQueue(d);

    const captured = d.controller.sessions.snapshot(OUTGOING) ?? '';
    assert.match(
      captured,
      /alpha one/,
      `a resetTerminal in the switch tick truncated the outgoing session's snapshot to ${JSON.stringify(captured)}`,
    );
  }, driver);
});

// Close shape: the snapshot survives and the switch still clears the pane.
test('a direct resetTerminal in the close tick still clears the pane', async () => {
  const { setSessionId } = await import('./slices/terminalSlice');
  const driver = await mountDriver();
  await withControllerDriven(async (d) => {
    await activateOutgoing(d);
    await seedOutgoingScreen(d);

    d.store.dispatch(setSessionId(0));
    d.controller.resetTerminal();
    await drainWriteQueue(d);

    assert.match(
      d.controller.sessions.snapshot(OUTGOING) ?? '',
      /alpha one/,
      'the close must still capture the outgoing session screen',
    );
    assert.equal(d.serialize.serialize().trim(), '', 'the closed pane must end up cleared');
  }, driver);
});

// Past MAX_RETAINED_BYTES every append trims the head, moving the buffer under
// any boundary taken before the trim.
const CAP_CHUNK_BYTES = 120_000;
const CAP_SEED_CHUNKS = 20;

// Marker last: only the final scrollback of a ~1500-row line is on the pane.
function capSizedChunk(marker: string): string {
  return `${'x'.repeat(CAP_CHUNK_BYTES - marker.length - 2)}${marker}\r\n`;
}

function emitSessionOutput(driver: Driver, sessionId: number, text: string): void {
  const payload = { sessionId, data: Buffer.from(text, 'utf8').toString('base64') };
  (
    driver.controller as unknown as { handleTerminalOutput: (p: typeof payload) => void }
  ).handleTerminalOutput(payload);
}

// Output for the outgoing session mid-switch exists only in the buffer; a
// length-index boundary would lose it to the head trim.
test('output that arrives while a switch is in flight survives the head trim', async () => {
  const { setSessionId } = await import('./slices/terminalSlice');
  const IN_WINDOW_MARKER = 'in-window-marker';
  const driver = await mountDriver();
  await withControllerDriven(async (d) => {
    await activateOutgoing(d);
    for (let i = 0; i < CAP_SEED_CHUNKS; i += 1) {
      emitSessionOutput(d, OUTGOING, capSizedChunk(`seed-${String(i)}`));
    }
    await drainWriteQueue(d);
    const settledChunks = d.controller.sessions.displayBuffer(OUTGOING).length;
    assert.ok(
      settledChunks > 0,
      'the seeded session must have retained output for the switch to carry a boundary over',
    );

    d.store.dispatch(setSessionId(INCOMING));
    // The switch callback has not run yet; otherwise this case proves nothing.
    assert.equal(
      d.controller.sessions.displayBuffer(OUTGOING).length,
      settledChunks,
      'the switch must not have run its snapshot before this point, or there is no window to test',
    );
    emitSessionOutput(d, OUTGOING, capSizedChunk(IN_WINDOW_MARKER));
    await drainWriteQueue(d);

    const keptChunks = d.controller.sessions.displayBuffer(OUTGOING).length;
    d.store.dispatch(setSessionId(OUTGOING));
    await drainWriteQueue(d);
    assert.match(
      d.serialize.serialize(),
      new RegExp(IN_WINDOW_MARKER),
      'the pane of the session that was switched away from must still carry the output it produced during the switch',
    );
    assert.equal(
      keptChunks,
      1,
      'the output that arrived after the switch was dispatched is not in the buffer it has to be replayed from',
    );
  }, driver);
});
