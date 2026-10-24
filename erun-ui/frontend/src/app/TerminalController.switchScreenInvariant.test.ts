// A settled switch must leave the landed session's screen (marker and log tail)
// in the pane, activated from its snapshot.
//
// Waits are on observables (xterm's empty-write barrier and the switch queue's
// state), never on durations: a pane is blank between reset and paint. `split`
// defers content writes a macrotask, as a browser splits xterm's parse chain;
// `typed` sets `_didUserInput`, letting the barrier drain synchronously; `gap`
// names the interleaving of the pair's second click.
//
// Not covered: sessions the app respawns or re-selects mid-switch (Playwright).
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

// Only turns "never" into a failure rather than a hang; not a paint estimate.
const PANE_WAIT_TIMEOUT_MS = 10_000;

// Beyond a fixed 120ms read deadline, well inside PANE_WAIT_TIMEOUT_MS.
const HELD_PAINT_MS = 300;

type OutputHandler = (payload: TerminalOutputPayload) => void;

const nextEventLoopTurn = (): Promise<void> =>
  new Promise((resolve) => {
    setTimeout(resolve, 0);
  });

interface Driver {
  controller: TerminalController;
  pane: () => string;
  emit: (sessionId: number, text: string) => void;
  keystroke: () => void;
  renderedSessionId: () => number | null;
  pendingSwitch: () => number | null;
  // Content writes still held by a venue seam.
  pendingDeferredWrites: () => number;
  waitUntil: (condition: () => boolean) => Promise<boolean>;
  waitForLandedPane: (sessionId: number) => Promise<string>;
  // Does not read the pane back: serializing a large retained log is costly.
  settlePane: () => Promise<void>;
  holdContentWritesFor: (ms: number) => void;
  activation: () => { writes: number; source: string };
  unmount: () => Promise<void>;
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
  // Own root, so activation attributes come from this driver's switch.
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
  // Venue seams; both count toward pendingDeferredWrites.
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
  const settled = (): Promise<void> =>
    new Promise((resolve) => {
      terminal.write('', () => {
        resolve();
      });
    });
  // A barrier can resolve before a due switch writes anything, so a holding
  // condition is confirmed across a second drained turn.
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
    // xterm syncs its viewport on an animation frame after a write; dispose after it.
    unmount: async () => {
      await new Promise<void>((resolve) => {
        requestAnimationFrame(() => {
          resolve();
        });
      });
      unmount();
    },
  };
}

const failures: string[] = [];

interface PairOptions {
  split: boolean;
  typed: boolean;
  gap: number;
  holdPaintMs?: number;
  where: string;
}

// Returns the pair's failure reasons, empty on success.
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
    await driver.waitForLandedPane(extra);
    driver.controller.resetTerminal();
    driver.emit(extra, `${EXTRA_MARKER}\r\n`.repeat(4));
    await driver.settlePane();
    driver.emit(local, `${LOCAL_MARKER}\r\n`.repeat(6));
    driver.emit(
      extra,
      `${Array.from({ length: BULK_CHUNKS }, (_, i) => `${BULK_LINE} ${String(i)}`).join('\n')}\nBULK-END\n`,
    );
    if (options.typed) driver.keystroke();
    await driver.settlePane();
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
    // 0: switch to `local` still due (coalesces); 1: performed; 8: painted.
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
    await driver.unmount();
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

// Reproduces the gate failure (split=false, typed=true, gap=1) with a held paint
// instead of a loaded event loop.
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
