// A pane that is torn down and rebuilt has to come back on the session the
// store names.
//
// `unmountTerminal` drops the switch a teardown made unrunnable
// (TerminalSwitchQueue.cancel) and `mount` builds a fresh, blank xterm. What
// fills that pane is a `setSessionId` dispatch, and the middleware only
// forwards one when the store's session *changes* -- which tearing the pane
// down does not. So a reader whose window is rebuilt while they sit on a
// session is left on an empty terminal with the tab strip naming the session it
// should be showing, and nothing will ever repaint it: the pane is blank for
// good, and it is the same dead end as a switch that is dropped after it has
// cleared the pane.
//
// This drives the real controller on a real DOM with a real xterm and a real
// serializer, tears the pane down, rebuilds it, and asserts the pane comes back
// holding the session's screen -- with no dispatch between the two, which is
// exactly the state a rebuilt pane is left in.
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

const EXTRA_MARKER = 'EXTRA-PROMPT-MARKER';

const EXTRA = 501;

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

test('a remounted pane renders the session the store already names', async () => {
  const handlers = installBrowserGlobals();
  const controller = new TerminalController();
  const el = (tag: string, className = ''): HTMLElement => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    document.body.appendChild(node);
    return node;
  };
  const elements = () => ({
    terminalRoot: el('div', 'terminal') as HTMLDivElement,
    terminalPane: el('main'),
    reviewView: el('section'),
    reviewMain: el('div') as HTMLDivElement,
    diffList: el('div') as HTMLDivElement,
  });
  const emit = (sessionId: number, text: string): void => {
    const data = Buffer.from(text, 'utf8').toString('base64');
    for (const handler of handlers.get('terminal-output') ?? []) {
      handler({ sessionId, data });
    }
  };

  let unmount = controller.mount(elements());
  const reader = new SerializeAddon();
  (controller as unknown as { terminal: Terminal }).terminal.loadAddon(reader);

  store.dispatch(setSessionId(EXTRA));
  await sleep(10);
  controller.resetTerminal();
  emit(EXTRA, `${EXTRA_MARKER}\r\n`.repeat(4));
  await sleep(30);
  assert.ok(reader.serialize().includes(EXTRA_MARKER), 'setup: extra did not render');

  // The pane is torn down and rebuilt -- a layout change, a window re-open --
  // with the store still naming the session the reader was on, and nothing
  // dispatching a session id afterwards.
  unmount();
  await sleep(10);
  unmount = controller.mount(elements());
  const terminal = (controller as unknown as { terminal: Terminal }).terminal;
  const reader2 = new SerializeAddon();
  terminal.loadAddon(reader2);
  await sleep(60);

  const landed = reader2.serialize();
  unmount();
  assert.ok(
    landed.includes(EXTRA_MARKER),
    `the remounted pane stayed blank while the store names ${String(EXTRA)}: ${JSON.stringify(landed)}`,
  );
});
