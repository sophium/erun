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

// A driver over the real classes -- the real TerminalController mounted on a
// real DOM, the real terminalDisplayMiddleware reacting to real store
// dispatches, a real xterm instance and a real SerializeAddon -- and "the pane"
// read back out of xterm with a serializer, so what it asserts is what a reader
// would see rather than an inference from internal bookkeeping.
//
// The defect it pins: a reader clicking from one terminal tab to a second, with
// the very next click going back, delivers two setSessionId dispatches in the
// same task. Each one resets the shared xterm synchronously and queues its own
// activation's writes; activation 1's writes parse first, so screen 1 paints and
// screen 2 is appended beneath it instead of replacing it. The landed tab then
// shows the tab it was switched to *on top of* the screen of the tab it was
// switched from, and stays that way.

const LOCAL_SCREEN = 'LOCAL-PROMPT-MARKER\r\n'.repeat(6);
const EXTRA_SCREEN = 'EXTRA-PROMPT-MARKER\r\n'.repeat(4);

type OutputHandler = (payload: TerminalOutputPayload) => void;

interface Bridge {
  handlers: Map<string, OutputHandler[]>;
}

function installBrowserGlobals(): Bridge {
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
  // The Wails bridge is the transport boundary this driver stands in for: there
  // is no Go process behind it, it only hands back the subscriptions the app
  // registers, and every call it makes from the app is inert.
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
  // jsdom performs no layout, so the terminal keeps its constructed geometry and
  // no resize is ever reported: the observer's callbacks are inert.
  (globalThis as unknown as Record<string, unknown>).ResizeObserver = class {
    observe = noop;
    unobserve = noop;
    disconnect = noop;
  };
  return { handlers };
}

interface Driver {
  controller: TerminalController;
  pane: () => string;
  emit: (sessionId: number, text: string) => void;
  settled: () => Promise<void>;
  unmount: () => void;
}

function mountDriver(): Driver {
  const bridge = installBrowserGlobals();
  const controller = new TerminalController();
  const element = (tag: string): HTMLElement => {
    const created = document.createElement(tag);
    document.body.appendChild(created);
    return created;
  };
  const unmount = controller.mount({
    terminalRoot: element('div') as HTMLDivElement,
    terminalPane: element('main'),
    reviewView: element('section'),
    reviewMain: element('div') as HTMLDivElement,
    diffList: element('div') as HTMLDivElement,
  });
  // The controller owns the terminal it created; the driver reads the pane back
  // through a second real serializer loaded onto that same instance.
  const terminal = (controller as unknown as { terminal: Terminal }).terminal;
  const paneReader = new SerializeAddon();
  terminal.loadAddon(paneReader);
  // Settled = every write already queued for xterm has been parsed: the empty
  // write's completion callback fires only after the writes queued before it.
  // That observable replaces a delay everywhere this driver needs to wait.
  const settled = (): Promise<void> =>
    new Promise((resolve) => {
      terminal.write('', () => {
        resolve();
      });
    });
  return {
    controller,
    pane: () => paneReader.serialize(),
    emit: (sessionId, text) => {
      const data = Buffer.from(text, 'utf8').toString('base64');
      for (const handler of bridge.handlers.get('terminal-output') ?? []) {
        handler({ sessionId, data });
      }
    },
    settled,
    unmount,
  };
}

const EXTRA_MARKER = 'EXTRA-PROMPT-MARKER';
const LOCAL_MARKER = 'LOCAL-PROMPT-MARKER';

// One rapid pair: the reader is on `extra`, clicks `local`, and clicks back to
// `extra` `gapMs` later. A gap of 0 is the two dispatches with nothing awaited
// between them -- the fastest a pair of clicks can arrive -- and the larger gaps
// are the same pair with the first switch's paint given time to land.
async function measurePair(
  driver: Driver,
  gapMs: number,
): Promise<{ landedPane: string; paneBackOnLocal: string }> {
  const local = 200;
  const extra = local + 1;
  // Start on `extra`, painted.
  store.dispatch(setSessionId(extra));
  await driver.settled();
  driver.controller.resetTerminal();
  driver.emit(extra, EXTRA_SCREEN);
  await driver.settled();
  // `local` has its own output, buffered while another tab is on screen.
  driver.emit(local, LOCAL_SCREEN);

  store.dispatch(setSessionId(local));
  if (gapMs > 0) {
    await new Promise((resolve) => setTimeout(resolve, gapMs));
  }
  store.dispatch(setSessionId(extra));
  // The landed tab is `extra`. Wait for everything the pair queued to parse
  // before reading the pane: the screen is only final once xterm is idle.
  await driver.settled();
  const landedPane = driver.pane();
  // Then switch back to the tab the pair was switched away from, which must
  // still be able to render its own screen.
  store.dispatch(setSessionId(local));
  await driver.settled();
  const paneBackOnLocal = driver.pane();
  return { landedPane, paneBackOnLocal };
}

test('a rapid click pair lands with only the landed tab on screen', async () => {
  const driver = mountDriver();
  try {
    let gapMs = 0;
    for (gapMs of [0, 5, 25]) {
      const measured = await measurePair(driver, gapMs);
      const where = `gap=${gapMs.toString()}ms`;
      // The landed tab's own screen is on the pane...
      assert.ok(
        measured.landedPane.includes(EXTRA_MARKER),
        `${where}: the landed tab's own screen was not rendered: ${measured.landedPane}`,
      );
      // ...and the screen of the tab the pair switched away from is not: the
      // activation that lands replaces the outgoing screen rather than being
      // joined by it. An inclusion-only assertion is not this check -- the
      // joined pane carries both markers, four of one and six of the other.
      assert.ok(
        !measured.landedPane.includes(LOCAL_MARKER),
        `${where}: the landed pane carried the other tab's screen: ${measured.landedPane}`,
      );
      // The other direction: the tab switched away from still renders its own
      // screen, with none of the tab the pair landed on.
      assert.ok(
        measured.paneBackOnLocal.includes(LOCAL_MARKER),
        `${where}: the tab the pair switched away from lost its own screen: ${measured.paneBackOnLocal}`,
      );
      assert.ok(
        !measured.paneBackOnLocal.includes(EXTRA_MARKER),
        `${where}: the tab the pair switched away from showed the landed tab's screen: ${measured.paneBackOnLocal}`,
      );
    }
  } finally {
    // Let the terminal's own animation-frame work drain before tearing it down:
    // under jsdom a viewport sync scheduled by a write outlives the dispose and
    // touches renderer services that are already gone.
    await new Promise((resolve) => {
      requestAnimationFrame(() => {
        resolve(undefined);
      });
    });
    await new Promise((resolve) => {
      requestAnimationFrame(() => {
        resolve(undefined);
      });
    });
    driver.unmount();
  }
});
