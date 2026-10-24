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

// Drives the real controller, middleware, xterm and SerializeAddon. Pins: once a
// switch settles, the pane holds exactly the store's session's screen. A typed
// keystroke makes xterm drain synchronously, so cases run with and without one.

const LOCAL_SCREEN = 'LOCAL-PROMPT-MARKER\r\n'.repeat(6);
const EXTRA_SCREEN = 'EXTRA-PROMPT-MARKER\r\n'.repeat(4);
const FRESH_SCREEN = 'FRESH-PROMPT-MARKER\r\n'.repeat(3);

const LOCAL_MARKER = 'LOCAL-PROMPT-MARKER';
const EXTRA_MARKER = 'EXTRA-PROMPT-MARKER';
const FRESH_MARKER = 'FRESH-PROMPT-MARKER';

const LOCAL = 200;
const EXTRA = 201;
const FRESH = 202;

type OutputHandler = (payload: TerminalOutputPayload) => void;

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
  return handlers;
}

interface Driver {
  controller: TerminalController;
  pane: () => string;
  emit: (sessionId: number, text: string) => void;
  settled: () => Promise<void>;
  keystroke: () => void;
  unmount: () => void;
}

function mountDriver(): Driver {
  const handlers = installBrowserGlobals();
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
  return {
    controller,
    pane: () => paneReader.serialize(),
    emit: (sessionId, text) => {
      const data = Buffer.from(text, 'utf8').toString('base64');
      for (const handler of handlers.get('terminal-output') ?? []) {
        handler({ sessionId, data });
      }
    },
    // A switch writes from a write callback, so two barriers are needed.
    settled: async () => {
      for (let barrier = 0; barrier < 2; barrier += 1) {
        await new Promise<void>((resolve) => {
          terminal.write('', () => {
            resolve();
          });
        });
      }
    },
    keystroke: () => {
      const textarea = (terminal as unknown as { textarea?: HTMLTextAreaElement }).textarea;
      if (!textarea) {
        throw new Error('driver: xterm textarea not found');
      }
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
    unmount,
  };
}

async function tearDown(driver: Driver): Promise<void> {
  // Under jsdom a viewport sync scheduled by a write outlives dispose.
  for (let frame = 0; frame < 2; frame += 1) {
    await new Promise((resolve) => {
      requestAnimationFrame(() => {
        resolve(undefined);
      });
    });
  }
  driver.unmount();
}

async function openOn(driver: Driver, sessionId: number, screen: string): Promise<void> {
  store.dispatch(setSessionId(sessionId));
  await driver.settled();
  driver.controller.resetTerminal();
  driver.emit(sessionId, screen);
  await driver.settled();
}

function markers(pane: string, marker: string): number {
  return pane.split(marker).length - 1;
}

assert.equal(markers('aXbXc', 'X'), 2, 'test helper: marker counting');

test('a rapid click pair lands with only the landed tab on screen', async () => {
  const driver = mountDriver();
  const failures: string[] = [];
  try {
    for (const gapMs of [0, 5, 25]) {
      await openOn(driver, EXTRA, EXTRA_SCREEN);
      driver.emit(LOCAL, LOCAL_SCREEN);
      store.dispatch(setSessionId(LOCAL));
      if (gapMs > 0) {
        await new Promise((resolve) => setTimeout(resolve, gapMs));
      }
      store.dispatch(setSessionId(EXTRA));
      await driver.settled();
      const where = `gap=${String(gapMs)}ms`;
      // Inclusion alone would pass on a joined pane carrying both markers.
      const landed = driver.pane();
      if (!landed.includes(EXTRA_MARKER) || landed.includes(LOCAL_MARKER)) {
        failures.push(`${where}: landed pane wrong: ${landed}`);
      }
      store.dispatch(setSessionId(LOCAL));
      await driver.settled();
      const back = driver.pane();
      if (!back.includes(LOCAL_MARKER) || back.includes(EXTRA_MARKER)) {
        failures.push(`${where}: switch-back pane wrong: ${back}`);
      }
    }
    assert.deepEqual(failures, []);
  } finally {
    await tearDown(driver);
  }
});

const DELIVERIES: { name: string; deliver: (driver: Driver) => Promise<void> }[] = [
  {
    name: 'same task',
    deliver: async (driver) => {
      store.dispatch(setSessionId(LOCAL));
      store.dispatch(setSessionId(EXTRA));
      await driver.settled();
    },
  },
  {
    name: 'microtask',
    deliver: async (driver) => {
      store.dispatch(setSessionId(LOCAL));
      await Promise.resolve();
      store.dispatch(setSessionId(EXTRA));
      await driver.settled();
    },
  },
  {
    name: 'macrotask between',
    deliver: async (driver) => {
      store.dispatch(setSessionId(LOCAL));
      await new Promise((resolve) => setTimeout(resolve, 0));
      store.dispatch(setSessionId(EXTRA));
      await driver.settled();
    },
  },
  {
    name: 'macrotask first',
    deliver: async (driver) => {
      await new Promise((resolve) => setTimeout(resolve, 0));
      store.dispatch(setSessionId(LOCAL));
      store.dispatch(setSessionId(EXTRA));
      await driver.settled();
    },
  },
];

for (const delivery of DELIVERIES) {
  test(`two dispatches delivered in one ${delivery.name} lose no switch`, async () => {
    const driver = mountDriver();
    try {
      await openOn(driver, EXTRA, EXTRA_SCREEN);
      driver.emit(LOCAL, LOCAL_SCREEN);
      await delivery.deliver(driver);
      assert.equal(store.getState().terminal.sessionId, EXTRA);
      const landed = driver.pane();
      assert.ok(landed.includes(EXTRA_MARKER) && !landed.includes(LOCAL_MARKER), landed);
      store.dispatch(setSessionId(LOCAL));
      await driver.settled();
      const back = driver.pane();
      assert.ok(back.includes(LOCAL_MARKER) && !back.includes(EXTRA_MARKER), back);
    } finally {
      await tearDown(driver);
    }
  });
}

// Mirrors startCloudInitSession: setSessionId then resetTerminal(); a second
// clear after activation would wipe the new session's screen.
test('a new session with buffered output renders through the reset that follows its selection', async () => {
  const driver = mountDriver();
  try {
    await openOn(driver, EXTRA, EXTRA_SCREEN);
    driver.emit(FRESH, FRESH_SCREEN);
    driver.keystroke();
    store.dispatch(setSessionId(FRESH));
    driver.controller.resetTerminal();
    await driver.settled();
    const pane = driver.pane();
    assert.ok(pane.includes(FRESH_MARKER), `the new session rendered nothing: ${pane}`);
  } finally {
    await tearDown(driver);
  }
});

// Mirrors manageDeleteThunks: setSessionId(0) then reset.
test('a close after a session captures it and clears the pane', async () => {
  const driver = mountDriver();
  try {
    await openOn(driver, EXTRA, EXTRA_SCREEN);
    driver.emit(FRESH, FRESH_SCREEN);
    driver.keystroke();
    store.dispatch(setSessionId(FRESH));
    driver.controller.resetTerminal();
    await driver.settled();
    assert.ok(driver.pane().includes(FRESH_MARKER), 'setup: FRESH did not render');
    store.dispatch(setSessionId(0));
    driver.controller.resetTerminal();
    await driver.settled();
    assert.equal(store.getState().terminal.sessionId, 0);
    const pane = driver.pane();
    assert.ok(!pane.includes(FRESH_MARKER), `the dead session's screen survived: ${pane}`);
  } finally {
    await tearDown(driver);
  }
});

test('a close after a typed session clears the screen it is showing', async () => {
  const driver = mountDriver();
  try {
    await openOn(driver, EXTRA, EXTRA_SCREEN);
    driver.keystroke();
    store.dispatch(setSessionId(FRESH));
    driver.controller.resetTerminal();
    await driver.settled();
    driver.emit(FRESH, FRESH_SCREEN);
    await driver.settled();
    assert.ok(driver.pane().includes(FRESH_MARKER), 'setup: FRESH did not render');
    store.dispatch(setSessionId(0));
    driver.controller.resetTerminal();
    await driver.settled();
    assert.equal(store.getState().terminal.sessionId, 0);
    const pane = driver.pane();
    assert.ok(!pane.includes(FRESH_MARKER), `the dead session's screen survived: ${pane}`);
  } finally {
    await tearDown(driver);
  }
});

// The switch barrier may land inside the dispatch when the user has typed, so
// each sequence runs typed and untyped and the panes must agree.
const BARRIER_POSITIONS: {
  name: string;
  expected: 'fresh' | 'blank' | 'extra';
  run: (driver: Driver) => Promise<void>;
}[] = [
  {
    name: 'selection of a session with buffered output, then a reset',
    expected: 'fresh',
    run: async (driver) => {
      driver.emit(FRESH, FRESH_SCREEN);
      store.dispatch(setSessionId(FRESH));
      driver.controller.resetTerminal();
      await driver.settled();
    },
  },
  {
    name: 'a close after that selection',
    expected: 'blank',
    run: async (driver) => {
      driver.emit(FRESH, FRESH_SCREEN);
      store.dispatch(setSessionId(FRESH));
      driver.controller.resetTerminal();
      await driver.settled();
      store.dispatch(setSessionId(0));
      driver.controller.resetTerminal();
      await driver.settled();
    },
  },
  {
    name: 'a rapid pair',
    expected: 'extra',
    run: async (driver) => {
      driver.emit(LOCAL, LOCAL_SCREEN);
      store.dispatch(setSessionId(LOCAL));
      store.dispatch(setSessionId(EXTRA));
      await driver.settled();
    },
  },
];

for (const position of BARRIER_POSITIONS) {
  test(`the pane does not depend on where the write barrier lands: ${position.name}`, async () => {
    const panes: Record<'drainedSynchronously' | 'deferred', string> = {
      drainedSynchronously: '',
      deferred: '',
    };
    for (const typed of [true, false]) {
      const driver = mountDriver();
      try {
        await openOn(driver, EXTRA, EXTRA_SCREEN);
        if (typed) {
          driver.keystroke();
        }
        await position.run(driver);
        const read = driver.pane();
        if (typed) {
          panes.drainedSynchronously = read;
        } else {
          panes.deferred = read;
        }
      } finally {
        await tearDown(driver);
      }
    }
    assert.equal(
      panes.drainedSynchronously,
      panes.deferred,
      'the pane changed with where the barrier landed',
    );
    if (position.expected === 'fresh') {
      assert.ok(
        panes.deferred.includes(FRESH_MARKER),
        `the new session rendered nothing: ${panes.deferred}`,
      );
    } else if (position.expected === 'blank') {
      assert.ok(
        !panes.deferred.includes(FRESH_MARKER),
        `the dead session's screen survived: ${panes.deferred}`,
      );
    } else {
      assert.equal(markers(panes.deferred, EXTRA_MARKER), 4, panes.deferred);
      assert.equal(markers(panes.deferred, LOCAL_MARKER), 0, panes.deferred);
    }
  });
}
