// A switch that settles must leave the landed session's screen in the pane.
//
// The cases below drive the sequence a reader makes at speed -- switch away from
// a session holding a large retained log, then click straight back to it -- and
// read the pane back out through a real serializer on a real xterm. The landed
// session's screen (its marker *and* the tail of the log behind it) has to be
// there, and the activation that put it there has to name the snapshot path:
// a pane that comes back blank, stale, or re-fed from the log is the defect
// this pins, and it is the shape a reported gate failure took.
//
// Every wait below is on an observable, never on a duration. A pane is blank
// between a switch's reset and the macrotask its activation's writes are parsed
// on, so a case that reads it on a fixed deadline is asserting on the venue:
// a slow enough event loop puts that deadline inside the window and reds the
// file for a switch that is working (the shape a gate failure took -- the
// landed pane read back as ""). The two observables used instead are xterm's
// own write barrier -- an empty write's completion callback runs only once
// every chunk queued ahead of it has parsed, the same barrier
// TerminalSwitchQueue settles a switch behind -- and the switch queue's own
// account of the pane: which session it is recorded as holding, and whether a
// switch is still due. A wait ends as soon as the observable holds; its
// deadline only decides how long a case waits for a pane that never arrives,
// which still fails the case.
//
// The seam the other drivers in this suite do not model: a real browser splits
// xterm's parse chain. `WriteBuffer._innerWrite` breaks on a 12ms budget and
// resumes on a later macrotask, so an activation's writes can still be unparsed
// when the next dispatch arrives. jsdom parses far inside that budget and never
// expresses it, so `split` defers each content write by one macrotask and every
// case runs both ways. `typed` covers the other side of the same coin: a real
// keystroke sets xterm's `_didUserInput`, which lets the empty-write barrier
// drain synchronously inside the dispatch that armed it.
//
// `gap` is the interleaving the pair's second click lands in, named rather than
// timed: 0 is the two dispatches with nothing waited between them, so the
// switch to `local` is still due and the pair coalesces onto one switch; 1
// waits for that switch to be performed; 8 for its paint to have landed. Each
// is an observable the switch queue reports, and all three end on the same
// deterministic landing wait, so the dimension varies the interleaving without
// varying how long the case is willing to wait for the switch to work.
//
// The last case runs the reported pair on a venue whose activation paint lands
// later than the fixed deadline this file used to read on: it is the
// reproduction of the gate failure, and it is deterministic rather than
// load-dependent because the lateness is constructed instead of raced for.
//
// What this file does NOT reach: the dispatch a switch actually answers to here
// is a `setSessionId` from the store, so it cannot exercise a session that is
// respawned or re-selected by the app mid-switch. If the blank came from the
// store being pointed at a session with no screen to render, this file cannot
// see it -- it would need the app's own click path, i.e. Playwright.
//
// @vitest-environment jsdom
import assert from 'node:assert/strict';

import { SerializeAddon } from '@xterm/addon-serialize';
import type { Terminal } from '@xterm/xterm';
import { noop } from 'erun-kit';
import { test } from 'vitest';

import type { TerminalOutputPayload } from '@/types';

import { setSessionId } from './slices/terminalSlice';
import { store } from './store';
import { TerminalController } from './TerminalController';

const LOCAL_MARKER = 'LOCAL-PROMPT-MARKER';
const EXTRA_MARKER = 'EXTRA-PROMPT-MARKER';
const BULK_LINE = 'x'.repeat(400);
const BULK_CHUNKS = 60;

let LOCAL = 300;
let EXTRA = 301;

// How long a case waits for a pane that never reaches the state it is about to
// assert. The wait itself ends on the observable, so this is only what turns
// "never" into a failure rather than a hang -- it is not a guess at how long
// the switch takes, and it is deliberately far above any real paint.
const PANE_WAIT_TIMEOUT_MS = 10_000;

// The reproduction's stand-in for a venue slow enough to hold an activation's
// paint past the 120ms the reported case read on. Above that deadline and well
// inside the wait above, so it is the difference between the two that the case
// demonstrates.
const HELD_PAINT_MS = 300;

type OutputHandler = (payload: TerminalOutputPayload) => void;

// One turn of the event loop, so a timer the switch path scheduled -- or a
// held content write -- gets to run. Not a duration: nothing here asserts how
// long a turn takes.
const nextEventLoopTurn = (): Promise<void> =>
  new Promise((resolve) => {
    setTimeout(resolve, 0);
  });

interface Driver {
  controller: TerminalController;
  pane: () => string;
  emit: (sessionId: number, text: string) => void;
  keystroke: () => void;
  // The session the pane is recorded as holding, null when it holds nothing.
  renderedSessionId: () => number | null;
  // The session a switch is still due to land on, null when none is pending.
  pendingSwitch: () => number | null;
  // Content writes the split seam (or the reproduction's held paint) is still
  // holding before handing to xterm: until this is 0 the pane cannot have
  // caught up with them.
  pendingDeferredWrites: () => number;
  // Waits for an observable of the switch path, bounded -- see the waitUntil
  // this driver is built on.
  waitUntil: (condition: () => boolean) => Promise<boolean>;
  waitForLandedPane: (sessionId: number) => Promise<string>;
  // Waits for the pane to have taken everything it has been handed: no content
  // write held by a venue seam, no switch still due, and xterm's queue drained
  // behind them. Does not read the pane back -- a serialize of a retained log
  // this size is not free, and most callers only need the state.
  settlePane: () => Promise<void>;
  holdContentWritesFor: (ms: number) => void;
  activation: () => { writes: number; source: string };
  unmount: () => void;
}

interface SwitchQueueView {
  switchQueue: { paneSessionId: number | null; pendingSessionId: number | null };
}

function installBrowserGlobals(): Map<string, OutputHandler[]> {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => undefined,
      removeListener: () => undefined,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      dispatchEvent: () => false,
    }),
  });
  const handlers = new Map<string, OutputHandler[]>();
  (window as unknown as Record<string, unknown>).runtime = {
    EventsOnMultiple: (name: string, callback: OutputHandler) => {
      const registered = handlers.get(name) ?? [];
      registered.push(callback);
      handlers.set(name, registered);
      return () => undefined;
    },
    EventsOff: () => undefined,
    EventsOffAll: () => undefined,
    EventsEmit: () => undefined,
    LogPrint: () => undefined,
  };
  (window as unknown as Record<string, unknown>).go = {
    main: { App: new Proxy({}, { get: () => () => Promise.resolve(undefined) }) },
  };
  (globalThis as unknown as Record<string, unknown>).ResizeObserver = class {
    observe = noop;
    unobserve = noop;
    disconnect = noop;
  };
  return handlers;
}

function mount(split: boolean): Driver {
  const handlers = installBrowserGlobals();
  const controller = new TerminalController();
  const el = (tag: string, className = ''): HTMLElement => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    document.body.appendChild(node);
    return node;
  };
  // The pane's own root, held here so the activation attributes this driver
  // reads are the ones its own switch published. Reaching for the first
  // `.terminal` in the document instead reads whichever case mounted first,
  // whose last activation says nothing about this one.
  const root = el('div', 'terminal') as HTMLDivElement;
  const unmount = controller.mount({
    terminalRoot: root,
    terminalPane: el('main'),
    reviewView: el('section'),
    reviewMain: el('div') as HTMLDivElement,
    diffList: el('div') as HTMLDivElement,
  });
  const terminal = (controller as unknown as { terminal: Terminal }).terminal;
  const reader = new SerializeAddon();
  terminal.loadAddon(reader);
  const queue = (controller as unknown as SwitchQueueView).switchQueue;
  let pendingDeferredWrites = 0;
  let holdContentWritesMs = 0;
  // The venue seams. `split` defers every content write by one macrotask, the
  // way a browser splits xterm's parse chain; `holdContentWritesFor` defers
  // them by a duration instead, which is the reproduction's stand-in for a
  // venue whose paint lands after a fixed deadline. Both count against
  // pendingDeferredWrites, so a wait for the landing ends on the paint and not
  // on the seam.
  const handToXterm = terminal.write.bind(terminal);
  terminal.write = (data: string, cb?: () => void): void => {
    const isContent = typeof data === 'string' && data.length > 0;
    if (!isContent || (!split && holdContentWritesMs === 0)) {
      handToXterm(data, cb);
      return;
    }
    const delay = holdContentWritesMs;
    pendingDeferredWrites += 1;
    setTimeout(() => {
      pendingDeferredWrites -= 1;
      handToXterm(data, cb);
    }, delay);
  };
  // An empty write's completion callback fires only once every chunk queued
  // ahead of it has parsed: xterm's own "the pane has caught up" signal.
  const settled = (): Promise<void> =>
    new Promise((resolve) => {
      terminal.write('', () => {
        resolve();
      });
    });
  // Waits for the condition the case is about to assert, on the two observables
  // that order it: an event-loop turn, so a switch's own timer can run, and a
  // write barrier, so everything already handed to xterm has parsed. A barrier
  // can resolve before a switch that was still due has issued anything, so a
  // condition that holds is confirmed across a second drained turn rather than
  // trusted from the first.
  const waitUntil = async (condition: () => boolean): Promise<boolean> => {
    const deadline = Date.now() + PANE_WAIT_TIMEOUT_MS;
    for (;;) {
      await nextEventLoopTurn();
      await settled();
      if (condition()) {
        await nextEventLoopTurn();
        await settled();
        if (condition()) {
          return true;
        }
      }
      if (Date.now() >= deadline) {
        return condition();
      }
    }
  };
  return {
    controller,
    pane: () => reader.serialize(),
    emit: (sessionId: number, text: string) => {
      const data = Buffer.from(text, 'utf8').toString('base64');
      for (const handler of handlers.get('terminal-output') ?? []) {
        handler({ sessionId, data });
      }
    },
    keystroke: () => {
      const textarea = (terminal as unknown as { textarea?: HTMLTextAreaElement }).textarea;
      if (!textarea) throw new Error('no textarea');
      textarea.dispatchEvent(
        new KeyboardEvent('keydown', {
          key: 'a',
          code: 'KeyA',
          keyCode: 65,
          which: 65,
          bubbles: true,
          cancelable: true,
        }),
      );
    },
    renderedSessionId: () => queue.paneSessionId,
    pendingSwitch: () => queue.pendingSessionId,
    pendingDeferredWrites: () => pendingDeferredWrites,
    waitUntil,
    waitForLandedPane: async (sessionId: number): Promise<string> => {
      await waitUntil(
        () =>
          queue.pendingSessionId === null &&
          queue.paneSessionId === sessionId &&
          pendingDeferredWrites === 0,
      );
      return reader.serialize();
    },
    settlePane: async (): Promise<void> => {
      await waitUntil(() => pendingDeferredWrites === 0 && queue.pendingSessionId === null);
    },
    holdContentWritesFor: (ms: number) => {
      holdContentWritesMs = ms;
    },
    activation: () => ({
      writes: Number(root.dataset.terminalActivationWrites ?? '-1'),
      source: root.dataset.terminalActivationSource ?? 'none',
    }),
    unmount: () => {
      unmount();
    },
  };
}

const failures: string[] = [];

interface PairOptions {
  split: boolean;
  typed: boolean;
  gap: number;
  // When set, the measured pair runs on a venue whose activation paint is held
  // this long -- see HELD_PAINT_MS.
  holdPaintMs?: number;
  where: string;
}

// One measured click pair: land on `extra` with its retained log painted,
// click `local`, then click straight back to `extra`. Returns the reasons the
// pair failed, empty when the landed pane holds `extra`'s screen and the
// activation that put it there restored a snapshot.
async function measurePair(options: PairOptions): Promise<string[]> {
  LOCAL += 10;
  EXTRA += 10;
  const driver = mount(options.split);
  const local = LOCAL;
  const extra = EXTRA;
  const found: string[] = [];
  const where = options.where;
  try {
    store.dispatch(setSessionId(extra));
    // The switch onto the pane has landed before anything is fed to it.
    await driver.waitForLandedPane(extra);
    driver.controller.resetTerminal();
    driver.emit(extra, `${EXTRA_MARKER}\r\n`.repeat(4));
    await driver.settlePane();
    // `local`'s output arrives while another tab is on screen: not rendered,
    // only buffered, and replayed by the switch that lands on it below.
    driver.emit(local, `${LOCAL_MARKER}\r\n`.repeat(6));
    driver.emit(
      extra,
      `${Array.from({ length: BULK_CHUNKS }, (_, i) => `${BULK_LINE} ${String(i)}`).join('\n')}\nBULK-END\n`,
    );
    if (options.typed) driver.keystroke();
    await driver.settlePane();
    // Land on extra (the drain), then the measured pair back to extra.
    store.dispatch(setSessionId(extra));
    await driver.settlePane();
    const staged = driver.pane();
    if (!staged.includes('BULK-END')) {
      found.push(`${where}: staging: drain did not render`);
      return found;
    }
    if (options.holdPaintMs !== undefined) {
      driver.holdContentWritesFor(options.holdPaintMs);
    }
    if (options.typed) driver.keystroke();
    store.dispatch(setSessionId(local));
    // The interleaving the second click lands in: 0 leaves the switch to
    // `local` still due, so the pair coalesces onto one switch; 1 waits for
    // that switch to be performed; 8 waits for its paint to have landed.
    if (options.gap > 0) {
      await driver.waitUntil(() => driver.pendingSwitch() === null);
    }
    if (options.gap > 1) {
      await driver.waitForLandedPane(local);
    }
    store.dispatch(setSessionId(extra));
    const landed = await driver.waitForLandedPane(extra);
    if (!landed.includes(EXTRA_MARKER) || !landed.includes('BULK-END')) {
      const owned = driver.renderedSessionId();
      found.push(
        `${where}: landed pane lost extra: ${landed.slice(-160)} ` +
          `[pane owned by ${owned === null ? 'nothing' : String(owned)}, ` +
          `${String(driver.pendingDeferredWrites())} writes still held]`,
      );
    }
    const act = driver.activation();
    if (act.source !== 'snapshot') {
      found.push(`${where}: activation source=${act.source} writes=${String(act.writes)}`);
    }
  } finally {
    driver.unmount();
  }
  return found;
}

for (const split of [false, true]) {
  for (const typed of [false, true]) {
    for (const gap of [0, 1, 8]) {
      test(`matrix split=${String(split)} typed=${String(typed)} gap=${String(gap)}`, async () => {
        failures.push(
          ...(await measurePair({
            split,
            typed,
            gap,
            where: `split=${String(split)} typed=${String(typed)} gap=${String(gap)}`,
          })),
        );
      });
    }
  }
}

// The reported gate failure, reproduced rather than raced for. The case is the
// one the gate red on -- split=false, typed=true, gap=1 -- run on a venue whose
// activation paint lands HELD_PAINT_MS after it is issued, which is what a
// loaded event loop did to it: the pane is read inside the reset->paint window
// and serializes to "". Reading on a fixed deadline reds here; waiting on the
// landing does not, which is the whole point of the waits above.
test('a pair whose paint lands late still lands on the extra screen', async () => {
  failures.push(
    ...(await measurePair({
      split: false,
      typed: true,
      gap: 1,
      holdPaintMs: HELD_PAINT_MS,
      where: `held paint=${String(HELD_PAINT_MS)}ms`,
    })),
  );
});

test('matrix summary', () => {
  assert.deepEqual(failures, []);
});
