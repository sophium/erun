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
// The ownership rule these cases pin: after every switch settles, the pane
// holds exactly one session's screen, and it is the session the store names.
//
// Two delays matter and neither is wall-clock. `settled()` is xterm's own idle
// point (an empty write's completion callback fires once every chunk queued
// ahead of it has parsed). The keystroke is the other one: xterm's write buffer
// drains synchronously when it is empty and the user has typed since the last
// drain, so a real keydown is what makes the switch's barrier land inside the
// dispatch instead of a task later. Every case that could care which way that
// went runs with the keystroke and without it.

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
  // The controller owns the terminal it created; the driver reads the pane back
  // through a second real serializer loaded onto that same instance.
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
    // Settled = every write already queued for xterm has been parsed.
    settled: () =>
      new Promise((resolve) => {
        terminal.write('', () => {
          resolve();
        });
      }),
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
  // Let the terminal's own animation-frame work drain before tearing it down:
  // under jsdom a viewport sync scheduled by a write outlives the dispose and
  // touches renderer services that are already gone.
  for (let frame = 0; frame < 2; frame += 1) {
    await new Promise((resolve) => {
      requestAnimationFrame(() => {
        resolve(undefined);
      });
    });
  }
  driver.unmount();
}

// Puts `screen` on `sessionId`, leaves the store and the pane on it, and leaves
// any other session's output buffered rather than rendered.
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
      // The landed tab's own screen is on the pane, and the tab the pair
      // switched away from is not: an inclusion-only check would pass on the
      // joined pane, which carries both markers, four of one and six of the
      // other.
      const landed = driver.pane();
      if (!landed.includes(EXTRA_MARKER) || landed.includes(LOCAL_MARKER)) {
        failures.push(`${where}: landed pane wrong: ${landed}`);
      }
      // And the tab the pair switched away from still renders its own screen.
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

// Four ways a reader's two clicks can reach the store. Whatever the spacing,
// the pane must end on the last session named and the store must agree.
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

// globalConfigCloudThunks.startCloudInitSession: startSession() has already been
// awaited when it dispatches setSessionId for the new session, so that session's
// output can already be buffered, and it calls resetTerminal() on the very next
// statement. The clear it asks for is the one the switch performs between
// capturing the outgoing screen and activating the incoming one -- a second
// clear landing after that activation wipes the screen the reader is waiting
// for, and the new session renders nothing.
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

// manageDeleteThunks: deleting the selected environment dispatches
// setSessionId(0) and resets. The close has to capture the outgoing session's
// screen, clear the pane, and leave nothing of the dead session behind.
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

// The close that follows a reader's first typing in the new session. The new
// session's output arrives live (it was not buffered before the switch), so the
// pane really holds it by the time the delete lands -- and the close has to
// capture and clear it rather than collapsing on what the pane is recorded as
// holding.
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

// The constraint the switch ordering is built around: xterm drains an empty-buffer
// write synchronously when the user has typed since the last drain, so the
// barrier that serializes a switch can land inside the dispatch that armed it.
// Nothing may key off which way that went, so each sequence runs twice -- typed
// and not -- and the two panes have to agree.
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
