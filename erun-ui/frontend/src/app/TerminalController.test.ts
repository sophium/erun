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
// schedules its drain with setTimeout -- and that drain re-arms itself while it
// still has data, so a fixed number of turns is only a guess at when a pane of
// a given size has caught up. The empty write is the observable condition
// instead, and the app's own ordering primitive (switchSession uses it for
// exactly this): its callback runs from xterm's queue once every chunk queued
// ahead of it has parsed. Nothing here is timed.
//
// Twice, because one callback is queued before any write a previous callback
// issues. A switch's own callback is what snapshots the outgoing session and
// activates the incoming one, so the replay it writes is queued behind the
// empty write's callback -- the first drain can resolve with that replay still
// unparsed, and the second lands behind it. One level of callbacks issuing
// writes is all this file has, and each empty write is guaranteed to wait for
// everything ahead of it however many times the queue yields.
async function drainWriteQueue(driver: Driver): Promise<void> {
  for (let pass = 0; pass < 2; pass += 1) {
    await new Promise<void>((resolve) => {
      driver.terminal.write('', () => {
        resolve();
      });
    });
  }
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
  await drainWriteQueue(driver);
}

// Brings the store onto OUTGOING the way any tab switch does, then lets that
// switch drain, so the pane is settled and the next dispatch's previous session
// is the one whose screen these cases are about.
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
    await drainWriteQueue(d);

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
    await drainWriteQueue(d);

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
    await drainWriteQueue(d);

    assert.match(
      d.controller.sessions.snapshot(OUTGOING) ?? '',
      /alpha one/,
      'the close must still capture the outgoing session screen',
    );
    assert.equal(d.serialize.serialize().trim(), '', 'the closed pane must end up cleared');
  }, driver);
});

// The byte budget MAX_RETAINED_BYTES bounds one session's retained output, and
// trimChunksToBudget keeps the *tail* up to it -- so a session that has crossed
// the budget sits just over it and every later append crosses it again and
// trims the head once more. That is the standing state of any long-running
// build or deploy log, and it is the state this case drives: one where the
// buffer's head moves under a boundary taken before the trim.
const CAP_CHUNK_BYTES = 120_000;
const CAP_SEED_CHUNKS = 20;

// One budget-sized, newline-terminated chunk: large enough that a single one
// crosses the byte budget on its own, and shaped so the byte budget is the one
// that trips (MAX_RETAINED_LINES is three orders of magnitude away).
//
// The marker goes last: a chunk this size is one line of ~1500 wrapped rows,
// and only the last scrollback's worth of rows is on the pane after a replay,
// so the end of the chunk is where a reader would find it.
function capSizedChunk(marker: string): string {
  return `${'x'.repeat(CAP_CHUNK_BYTES - marker.length - 2)}${marker}\r\n`;
}

// The real output path, not a shortcut to the buffer: the terminal-output
// event's own payload shape, through the same handler TerminalWailsEvents
// subscribes to it. This is what makes the case below a statement about what
// the desktop does with output that arrives mid-switch, rather than about one
// method called directly.
function emitSessionOutput(driver: Driver, sessionId: number, text: string): void {
  const payload = { sessionId, data: Buffer.from(text, 'utf8').toString('base64') };
  (
    driver.controller as unknown as { handleTerminalOutput: (p: typeof payload) => void }
  ).handleTerminalOutput(payload);
}

// Output that arrives for the session being left, inside the switch's window --
// after switchSession has taken its boundary and queued the hand-off on xterm's
// own queue, before that hand-off has run. The store no longer names that
// session, so handleTerminalOutput buffers this without writing it: it is in no
// snapshot, and the buffer is the only place it can survive.
//
// On the pre-fix code the boundary is the buffer's length at dispatch, taken as
// an index. The trim this chunk causes moves the head under it, slice() returns
// nothing, and captureSnapshot reads that as "nothing arrived" and deletes the
// entry -- the line is gone from the session for good, and its pane is missing
// output the shell really produced.
test('output that arrives while a switch is in flight survives the head trim', async () => {
  const { setSessionId } = await import('./slices/terminalSlice');
  const IN_WINDOW_MARKER = 'in-window-marker';
  const driver = await mountDriver();
  await withControllerDriven(async (d) => {
    await activateOutgoing(d);
    // A busy session at its cap: the pane carries what the shell wrote, and the
    // retained buffer is at the budget, so the next chunk trims it.
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
    // The window is open: the switch has taken its boundary but its callback
    // has not run, which is why the buffer is still whole here. If it were not,
    // the emit below would be written to the pane and snapshotted with it, and
    // this case would prove nothing.
    assert.equal(
      d.controller.sessions.displayBuffer(OUTGOING).length,
      settledChunks,
      'the switch must not have run its snapshot before this point, or there is no window to test',
    );
    emitSessionOutput(d, OUTGOING, capSizedChunk(IN_WINDOW_MARKER));
    await drainWriteQueue(d);

    const keptChunks = d.controller.sessions.displayBuffer(OUTGOING).length;
    // Switch back: the pane is the snapshot xterm took plus that delta, and
    // nothing else can bring the line back.
    d.store.dispatch(setSessionId(OUTGOING));
    await drainWriteQueue(d);
    assert.match(
      d.serialize.serialize(),
      new RegExp(IN_WINDOW_MARKER),
      'the pane of the session that was switched away from must still carry the output it produced during the switch',
    );
    // The same loss, one step upstream: without the delta there is nothing for
    // a later switch back to replay either.
    assert.equal(
      keptChunks,
      1,
      'the output that arrived after the switch was dispatched is not in the buffer it has to be replayed from',
    );
  }, driver);
});
