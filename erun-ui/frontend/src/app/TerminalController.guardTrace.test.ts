// @vitest-environment jsdom
import assert from 'node:assert/strict';

import type { Terminal } from '@xterm/xterm';
import { noop } from 'erun-kit';
import { afterEach, test, vi } from 'vitest';

import type { TerminalOutputPayload } from '@/types';

import { setSessionId } from './slices/terminalSlice';
import { store } from './store';
import { TerminalController } from './TerminalController';

// handleTerminalOutput's two guard returns were bare `return;`, so a skipped
// render left no record anywhere: narrowing a red on the switch-timing spec
// took an instrumented run and a hand-written probe, and the discriminator that
// finally separated the two exits was *which of them had been taken*, which
// neither said.
//
// Neither return loses output -- both follow the buffer appends, and the next
// activation replays what they buffered -- so there is no failure to
// reproduce here. What these cases pin is the trace itself: each guard is
// driven on a real controller and the line it emits is asserted, because the
// trace only pays off if a later refactor cannot quietly drop it.
//
// The two guards are reached two different ways, and the difference is the
// point. The session-mismatch guard is on the ordinary path -- every chunk a
// background session emits passes through it -- so its case drives the real
// subscription. The no-terminal guard is not: `unmountTerminal` detaches that
// subscription in the same call that nulls the pane, so the only caller that
// can still reach it is the cursor-restore timer, which that teardown never
// cancels. Its case drives exactly that, and would fail if the timer were
// cancelled at unmount -- the guard would then have no live path at all.

const ACTIVE = 300;
const OTHER = 301;

const HIDE_CURSOR = '\x1b[?25l';

// Both lines start with this, which is what makes a gate log greppable for
// them and what the assertions below read back.
const TRACE_PREFIX = 'erun-terminal: guard=';

// The cursor-restore timer's own delay, which the no-terminal case advances to
// rather than waiting out (TerminalController.CURSOR_RESTORE_DELAY_MS).
const CURSOR_RESTORE_MS = 250;

type OutputHandler = (payload: TerminalOutputPayload) => void;

// installBrowserGlobals stands in for the Wails bridge: it hands back the
// subscriptions the app registers and makes every call the app makes from it
// inert, so what the driver drives is the real controller on a real DOM rather
// than a substitute for either.
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
  // jsdom performs no layout, so the terminal keeps its constructed geometry
  // and no resize is ever reported: the observer's callbacks are inert.
  (globalThis as unknown as Record<string, unknown>).ResizeObserver = class {
    observe = noop;
    unobserve = noop;
    disconnect = noop;
  };
  return handlers;
}

interface Driver {
  paneIsAttached: () => boolean;
  emit: (sessionId: number, text: string) => void;
  settled: () => Promise<void>;
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
  // The controller owns the terminal it created; the driver only needs a
  // handle to settle on (see `settled` below), so it takes the one `mount`
  // just built rather than reaching for it again on every call.
  const terminal = (controller as unknown as { terminal: Terminal }).terminal;
  return {
    paneIsAttached: () =>
      (controller as unknown as { terminal: Terminal | null }).terminal !== null,
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

// captureWarnings collects everything the console is warned with for the
// duration of one case; guardTraces narrows that to the two guards' own lines
// so unrelated noise cannot decide an assertion either way.
function captureWarnings(): string[] {
  const lines: string[] = [];
  vi.spyOn(console, 'warn').mockImplementation((...args: unknown[]) => {
    lines.push(args.map((arg) => String(arg)).join(' '));
  });
  return lines;
}

function guardTraces(lines: string[]): string[] {
  return lines.filter((line) => line.startsWith(TRACE_PREFIX));
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  // The store is a module singleton, so a case that leaves it on its own
  // session decides the next case's starting point unless it is parked.
  store.dispatch(setSessionId(0));
});

test('output skipped for a session the store is not on names the guard and both ids', async () => {
  const driver = mountDriver();
  try {
    store.dispatch(setSessionId(ACTIVE));
    await driver.settled();

    const warnings = captureWarnings();
    driver.emit(OTHER, 'background-chunk\r\n');
    await driver.settled();

    assert.deepEqual(guardTraces(warnings), [
      `erun-terminal: guard=session-mismatch payloadSessionId=${String(OTHER)} activeSessionId=${String(ACTIVE)}`,
    ]);
  } finally {
    await tearDown(driver);
  }
});

test('a write with no pane attached names the guard and the session it was for', async () => {
  const driver = mountDriver();
  let warned: string[] = [];
  try {
    store.dispatch(setSessionId(ACTIVE));
    await driver.settled();
    const warnings = captureWarnings();

    // Only the timeout is faked, so xterm's own scheduling is untouched by the
    // switch; the case is settled on xterm's idle point above, before this.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    // A hidden cursor arms the restore timer, and the pane is torn down with
    // that timer still armed -- the one path to the no-terminal guard.
    driver.emit(ACTIVE, HIDE_CURSOR);
    assert.equal(
      guardTraces(warnings).length,
      0,
      'the pane is attached, so nothing is skipped yet',
    );

    driver.unmount();
    assert.equal(driver.paneIsAttached(), false, 'the pane is gone with the teardown');
    vi.advanceTimersByTime(CURSOR_RESTORE_MS);
    warned = guardTraces(warnings);
  } finally {
    vi.useRealTimers();
    await tearDown(driver);
  }

  assert.deepEqual(warned, [
    `erun-terminal: guard=no-terminal targetSessionId=${String(ACTIVE)} activeSessionId=${String(ACTIVE)} replay=false`,
  ]);
});
