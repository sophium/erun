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

// A driver over the real classes: the real TerminalController mounted on a real
// DOM, the real terminalDisplayMiddleware reacting to real store dispatches, a
// real xterm instance and a real SerializeAddon. "The pane" is read back out of
// xterm with the same serializer the controller snapshots with, so a blank pane
// is a blank measurement rather than an inference from internal bookkeeping.
//
// The defect it pins: a reader clicking two terminal tabs faster than xterm can
// paint the first switch lands on the second tab with the first switch's writes
// still queued and unparsed. The switch away from that first tab then serializes
// a terminal whose buffer has not been written yet -- "" -- and remembers that
// as the tab's screen, discarding the buffered history that was the only other
// copy of it. Every later switch back to that tab restores the blank.

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
  // jsdom performs no layout, so the terminal keeps its constructed geometry
  // and no resize is ever reported: the observer's callbacks are inert.
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

interface PairMeasurement {
  rememberedLocal: string | undefined;
  paneBackOnLocal: string;
  // The tab the first click of the pair switched away from. That capture is a
  // single switch away from an already-painted session -- the delta capture a
  // settled switch is supposed to make -- so it must survive the pair intact.
  rememberedExtra: string | undefined;
}

// One rapid click pair: the reader is on `extra`, clicks `local`, then clicks
// back to `extra` `gapMs` later. A gap of 0 is the two dispatches with nothing
// awaited between them -- the fastest a pair of clicks can arrive -- and the
// larger gaps are the same pair with the first switch's paint given time to
// land, which is what a slower reader's clicks get for free.
async function measureRapidPair(
  driver: Driver,
  gapMs: number,
  local: number,
): Promise<PairMeasurement> {
  const extra = local + 1;
  // Start on `extra`, painted.
  store.dispatch(setSessionId(extra));
  await driver.settled();
  driver.controller.resetTerminal();
  driver.emit(extra, EXTRA_SCREEN);
  await driver.settled();
  // `local` has its own output, buffered while another tab is on screen.
  driver.emit(local, LOCAL_SCREEN);

  // The first click of the pair, and the switch that remembers `extra`'s screen.
  store.dispatch(setSessionId(local));
  if (gapMs > 0) {
    await new Promise((resolve) => setTimeout(resolve, gapMs));
  }
  // The second click: away from `local`, and this is where a blank capture is
  // taken if `local` has not been painted yet.
  store.dispatch(setSessionId(extra));
  const rememberedLocal = driver.controller.sessions.snapshot(local);
  await driver.settled();
  // Read once the pair has settled rather than at the dispatch that causes it: a
  // switch captures the outgoing screen from xterm's own queue, so `extra`'s
  // capture is written after every write queued at the first click has parsed --
  // later than that dispatch returns, and always before here. Waiting costs the
  // case nothing, because the second click captures `local`, so this still names
  // the first click's capture and still fails if there is none.
  const rememberedExtra = driver.controller.sessions.snapshot(extra);
  // The reader lands on `extra`; then goes back to `local`, which is where the
  // blank screen the pair remembered gets restored.
  store.dispatch(setSessionId(local));
  await driver.settled();
  const paneBackOnLocal = driver.pane();
  return { rememberedLocal, paneBackOnLocal, rememberedExtra };
}

test('a rapid click pair never remembers a blank screen for the tab it switched to', async () => {
  const driver = mountDriver();
  try {
    let local = 100;
    for (const gapMs of [0, 5, 25]) {
      local += 10;
      const measured = await measureRapidPair(driver, gapMs, local);
      // The reported defect, in the exact state the report describes: the
      // second click arrives before the first switch's writes have parsed, the
      // capture comes back empty, and that emptiness becomes the tab's screen.
      assert.notEqual(
        measured.rememberedLocal,
        '',
        `gap=${gapMs.toString()}ms: an empty serialization was stored as the tab's remembered screen`,
      );
      // The reader's own view: switching back to the tab the pair clicked to
      // must render that tab's screen -- the buffered history survives to be
      // replayed when no capture could be taken.
      assert.ok(
        measured.paneBackOnLocal.includes('LOCAL-PROMPT-MARKER'),
        `gap=${gapMs.toString()}ms: the pane was blank on the switch back to the tab that was clicked to`,
      );
      // The other direction: the tab the pair switched away from still has its
      // own screen remembered, so refusing a capture did not cost the delta
      // capture a settled switch is supposed to make.
      assert.ok(
        (measured.rememberedExtra ?? '').includes('EXTRA-PROMPT-MARKER'),
        `gap=${gapMs.toString()}ms: the tab the pair switched away from lost its remembered screen`,
      );
    }
  } finally {
    // Let the terminal's own animation-frame work drain before tearing it
    // down: under jsdom a viewport sync scheduled by a write outlives the
    // dispose and touches renderer services that are already gone.
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
