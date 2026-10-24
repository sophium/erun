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

// Drives the real controller, middleware, xterm and SerializeAddon. Pins: a
// second click before the first switch's writes parse must not remember a blank
// ("") capture as the tab's screen.

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
  // Stand-in Wails bridge: no Go process, every call is inert.
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
  // jsdom performs no layout, so no resize is ever reported.
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
  const terminal = (controller as unknown as { terminal: Terminal }).terminal;
  const paneReader = new SerializeAddon();
  terminal.loadAddon(paneReader);
  // An empty write's callback fires after earlier writes parse. A switch writes
  // from such a callback, so two barriers are needed.
  const settled = async (): Promise<void> => {
    for (let barrier = 0; barrier < 2; barrier += 1) {
      await new Promise<void>((resolve) => {
        terminal.write('', () => {
          resolve();
        });
      });
    }
  };
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
  // A single switch away from a painted session, so it must survive the pair.
  rememberedExtra: string | undefined;
}

// gapMs 0 means both dispatches with nothing awaited between them.
async function measureRapidPair(
  driver: Driver,
  gapMs: number,
  local: number,
): Promise<PairMeasurement> {
  const extra = local + 1;
  store.dispatch(setSessionId(extra));
  await driver.settled();
  driver.controller.resetTerminal();
  driver.emit(extra, EXTRA_SCREEN);
  await driver.settled();
  driver.emit(local, LOCAL_SCREEN);

  store.dispatch(setSessionId(local));
  if (gapMs > 0) {
    await new Promise((resolve) => setTimeout(resolve, gapMs));
  }
  // A blank capture would be taken here if `local` has not painted yet.
  store.dispatch(setSessionId(extra));
  const rememberedLocal = driver.controller.sessions.snapshot(local);
  await driver.settled();
  // The capture is written from xterm's queue, after the dispatch returns, so read
  // it once settled.
  const rememberedExtra = driver.controller.sessions.snapshot(extra);
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
      assert.notEqual(
        measured.rememberedLocal,
        '',
        `gap=${gapMs.toString()}ms: an empty serialization was stored as the tab's remembered screen`,
      );
      assert.ok(
        measured.paneBackOnLocal.includes('LOCAL-PROMPT-MARKER'),
        `gap=${gapMs.toString()}ms: the pane was blank on the switch back to the tab that was clicked to`,
      );
      assert.ok(
        (measured.rememberedExtra ?? '').includes('EXTRA-PROMPT-MARKER'),
        `gap=${gapMs.toString()}ms: the tab the pair switched away from lost its remembered screen`,
      );
    }
  } finally {
    // Under jsdom a viewport sync scheduled by a write outlives dispose.
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
