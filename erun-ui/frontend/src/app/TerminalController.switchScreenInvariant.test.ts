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
// The seam the other drivers in this suite do not model: a real browser splits
// xterm's parse chain. `WriteBuffer._innerWrite` breaks on a 12ms budget and
// resumes on a later macrotask, so an activation's writes can still be unparsed
// when the next dispatch arrives. jsdom parses far inside that budget and never
// expresses it, so `split` defers each content write by one macrotask and every
// case runs both ways. `typed` covers the other side of the same coin: a real
// keystroke sets xterm's `_didUserInput`, which lets the empty-write barrier
// drain synchronously inside the dispatch that armed it.
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

type OutputHandler = (payload: TerminalOutputPayload) => void;
const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

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

function mount(split: boolean) {
  const handlers = installBrowserGlobals();
  const controller = new TerminalController();
  const el = (tag: string, className = ''): HTMLElement => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    document.body.appendChild(node);
    return node;
  };
  const unmount = controller.mount({
    terminalRoot: el('div', 'terminal') as HTMLDivElement,
    terminalPane: el('main'),
    reviewView: el('section'),
    reviewMain: el('div') as HTMLDivElement,
    diffList: el('div') as HTMLDivElement,
  });
  const terminal = (controller as unknown as { terminal: Terminal }).terminal;
  const reader = new SerializeAddon();
  terminal.loadAddon(reader);
  if (split) {
    const original = terminal.write.bind(terminal);
    terminal.write = (data: string, cb?: () => void) => {
      if (typeof data === 'string' && data.length > 0) {
        setTimeout(() => {
          original(data, cb);
        }, 0);
        return;
      }
      original(data, cb);
    };
  }
  return {
    controller,
    terminal,
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
    activation: () => {
      const root = document.querySelector<HTMLElement>('.terminal');
      return {
        writes: Number(root?.dataset.terminalActivationWrites ?? '-1'),
        source: root?.dataset.terminalActivationSource ?? 'none',
      };
    },
    unmount,
  };
}

const failures: string[] = [];

for (const split of [false, true]) {
  for (const typed of [false, true]) {
    for (const gap of [0, 1, 8]) {
      test(`matrix split=${String(split)} typed=${String(typed)} gap=${String(gap)}`, async () => {
        LOCAL += 10;
        EXTRA += 10;
        const driver = mount(split);
        const local = LOCAL;
        const extra = EXTRA;
        const where = `split=${String(split)} typed=${String(typed)} gap=${String(gap)}`;
        try {
          store.dispatch(setSessionId(extra));
          await sleep(15);
          driver.controller.resetTerminal();
          driver.emit(extra, `${EXTRA_MARKER}\r\n`.repeat(4));
          await sleep(15);
          driver.emit(local, `${LOCAL_MARKER}\r\n`.repeat(6));
          driver.emit(
            extra,
            `${Array.from({ length: BULK_CHUNKS }, (_, i) => `${BULK_LINE} ${String(i)}`).join('\n')}\nBULK-END\n`,
          );
          if (typed) driver.keystroke();
          await sleep(15);
          // Land on extra (the drain), then the measured pair back to extra.
          store.dispatch(setSessionId(extra));
          await sleep(60);
          if (!driver.pane().includes('BULK-END')) {
            failures.push(`${where}: staging: drain did not render`);
            return;
          }
          if (typed) driver.keystroke();
          store.dispatch(setSessionId(local));
          await sleep(gap);
          store.dispatch(setSessionId(extra));
          await sleep(120);
          const landed = driver.pane();
          if (!landed.includes(EXTRA_MARKER) || !landed.includes('BULK-END')) {
            failures.push(`${where}: landed pane lost extra: ${landed.slice(-160)}`);
          }
          const act = driver.activation();
          if (act.source !== 'snapshot') {
            failures.push(`${where}: activation source=${act.source} writes=${String(act.writes)}`);
          }
        } finally {
          await sleep(15);
          driver.unmount();
        }
      });
    }
  }
}

test('matrix summary', () => {
  assert.deepEqual(failures, []);
});
