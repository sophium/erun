// A rebuilt pane must restore the store's session without a new setSessionId
// dispatch: the middleware only forwards session changes.
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

// A switch writes from a write callback, so two barriers are needed.
async function settled(terminal: Terminal): Promise<void> {
  for (let barrier = 0; barrier < 2; barrier += 1) {
    await new Promise<void>((resolve) => {
      terminal.write('', resolve);
    });
  }
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
  const first = (controller as unknown as { terminal: Terminal }).terminal;
  const reader = new SerializeAddon();
  first.loadAddon(reader);

  store.dispatch(setSessionId(EXTRA));
  await settled(first);
  controller.resetTerminal();
  emit(EXTRA, `${EXTRA_MARKER}\r\n`.repeat(4));
  await settled(first);
  assert.ok(reader.serialize().includes(EXTRA_MARKER), 'setup: extra did not render');

  unmount();
  unmount = controller.mount(elements());
  const terminal = (controller as unknown as { terminal: Terminal }).terminal;
  const reader2 = new SerializeAddon();
  terminal.loadAddon(reader2);
  await settled(terminal);

  const landed = reader2.serialize();
  unmount();
  assert.ok(
    landed.includes(EXTRA_MARKER),
    `the remounted pane stayed blank while the store names ${String(EXTRA)}: ${JSON.stringify(landed)}`,
  );
});
