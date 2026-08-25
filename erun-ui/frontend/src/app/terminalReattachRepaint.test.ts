import assert from 'node:assert/strict';
import { afterEach, beforeEach, mock, test } from 'node:test';

import { noop } from 'erun-kit';

import { TerminalReattachRepaint } from './terminalReattachRepaint';

// TerminalReattachRepaint touches window only inside its methods -- never at
// module scope -- so a shim installed per test is enough to drive the real
// class. The generated Wails binding is the same: it dereferences window.go
// inside the call, so ResizeSession lands on the stub below.
const resizeCalls: [number, number, number][] = [];

function installWindow(): void {
  const shim = {
    // Unqualified globals, resolved at call time, so mock.timers is what runs.
    setTimeout: (handler: () => void, ms?: number) => setTimeout(handler, ms) as unknown as number,
    clearTimeout: (id: number) => {
      clearTimeout(id);
    },
    setInterval: (handler: () => void, ms?: number) =>
      setInterval(handler, ms) as unknown as number,
    clearInterval: (id: number) => {
      clearInterval(id);
    },
    go: {
      main: {
        App: {
          ResizeSession: (id: number, cols: number, rows: number) => {
            resizeCalls.push([id, cols, rows]);
            return Promise.resolve();
          },
        },
      },
    },
  };
  (globalThis as unknown as { window: unknown }).window = shim;
}

function lastResizeCall(): [number, number, number] | undefined {
  return resizeCalls[resizeCalls.length - 1];
}

let fitCount = 0;

function fakeTerminal(cols: number, rows: number) {
  const resizes: [number, number][] = [];
  return {
    cols,
    rows,
    resizes,
    // Every line reads empty: the state dtach leaves a reattached pane in, and
    // therefore the state the repaint cycle fires on.
    buffer: { active: { viewportY: 0, getLine: () => null } },
    resize(nextCols: number, nextRows: number) {
      this.cols = nextCols;
      this.rows = nextRows;
      resizes.push([nextCols, nextRows]);
    },
  };
}

function harness() {
  const terminal = fakeTerminal(120, 40);
  fitCount = 0;
  const repaint = new TerminalReattachRepaint({
    getTerminal: () => terminal as never,
    getFitAddon: () =>
      ({
        fit: () => {
          fitCount += 1;
        },
      }) as never,
    activeSessionId: () => 7,
    afterRestore: noop,
  });
  return { terminal, repaint };
}

beforeEach(() => {
  resizeCalls.length = 0;
  installWindow();
  mock.timers.enable({ apis: ['setTimeout', 'setInterval'] });
});

afterEach(() => {
  mock.timers.reset();
});

test('a keystroke restores the shrunken geometry at once, not mid-line 650ms later', () => {
  const { terminal, repaint } = harness();
  repaint.schedule(7);

  // The poller fires and shrinks. This is the window the operator hit.
  mock.timers.tick(1300);
  assert.equal(terminal.rows, 26, 'expected the shrink to be applied');
  assert.deepEqual(lastResizeCall(), [7, 120, 26]);

  // The user starts typing while it is shrunk.
  repaint.noteInput();
  assert.equal(terminal.rows, 40, 'input must restore the rows immediately');
  assert.deepEqual(lastResizeCall(), [7, 120, 40]);
  assert.equal(fitCount, 1, 'the restore must re-fit exactly once');

  // The original 650ms restore must not also land: a second reflow on a line
  // being edited is the corruption.
  const afterInput = terminal.resizes.length;
  mock.timers.tick(5000);
  assert.equal(terminal.resizes.length, afterInput, 'the cancelled hold must not fire');
  assert.equal(fitCount, 1, 'the cancelled hold must not re-fit again');
});

test('input disarms the poller, so no later cycle starts behind the user', () => {
  const { terminal, repaint } = harness();
  repaint.schedule(7);
  repaint.noteInput();

  mock.timers.tick(30000);
  assert.deepEqual(terminal.resizes, [], 'a pane being typed into must never be resized');
});

test('without input the cycle still completes, so the repaint is not simply disabled', () => {
  const { terminal, repaint } = harness();
  repaint.schedule(7);

  mock.timers.tick(1300);
  assert.equal(terminal.rows, 26);
  mock.timers.tick(650);
  assert.equal(terminal.rows, 40, 'the hold must restore on its own when nobody types');
  assert.equal(fitCount, 1);
});
