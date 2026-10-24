import assert from 'node:assert/strict';

import { noop } from 'erun-kit';
import { afterEach, beforeEach, test, vi } from 'vitest';

import { TerminalReattachRepaint } from './terminalReattachRepaint';

const resizeCalls: [number, number, number][] = [];

function installWindow(): void {
  const shim = {
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
  let content = '';
  return {
    cols,
    rows,
    resizes,
    setContent(next: string) {
      content = next;
    },
    // Empty by default: dtach leaves a reattached pane blank.
    buffer: {
      active: {
        viewportY: 0,
        getLine: () => (content === '' ? null : { translateToString: () => content }),
      },
    },
    resize(nextCols: number, nextRows: number) {
      this.cols = nextCols;
      this.rows = nextRows;
      resizes.push([nextCols, nextRows]);
    },
  };
}

function harness(
  proposeDimensions: () => { cols: number; rows: number } | undefined = () => ({
    cols: 120,
    rows: 40,
  }),
  afterRestore: () => void = noop,
) {
  const terminal = fakeTerminal(120, 40);
  fitCount = 0;
  const repaint = new TerminalReattachRepaint({
    getTerminal: () => terminal as never,
    getFitAddon: () =>
      ({
        proposeDimensions,
        fit: () => {
          fitCount += 1;
        },
      }) as never,
    activeSessionId: () => 7,
    afterRestore,
  });
  return { terminal, repaint };
}

beforeEach(() => {
  resizeCalls.length = 0;
  installWindow();
  vi.useFakeTimers({
    toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'Date'],
  });
});

afterEach(() => {
  vi.useRealTimers();
});

test('a keystroke restores the shrunken geometry at once, not mid-line 650ms later', () => {
  const { terminal, repaint } = harness();
  repaint.schedule(7);

  vi.advanceTimersByTime(1300);
  assert.equal(terminal.rows, 26, 'expected the shrink to be applied');
  assert.deepEqual(lastResizeCall(), [7, 120, 26]);

  repaint.noteInput();
  assert.equal(terminal.rows, 40, 'input must restore the rows immediately');
  assert.deepEqual(lastResizeCall(), [7, 120, 40]);
  assert.equal(fitCount, 1, 'the restore must re-fit exactly once');

  // The cancelled 650ms restore must not land; a second reflow mid-edit corrupts
  // the line.
  const afterInput = terminal.resizes.length;
  vi.advanceTimersByTime(700);
  assert.equal(terminal.resizes.length, afterInput, 'the cancelled hold must not fire');
  assert.equal(fitCount, 1, 'the cancelled hold must not re-fit again');
});

test('input defers the cycle through the quiet window but does not disarm it', () => {
  const { terminal, repaint } = harness();
  repaint.schedule(7);
  repaint.noteInput();

  vi.advanceTimersByTime(1300);
  assert.deepEqual(terminal.resizes, [], 'a tick within the quiet window must not resize');

  // The poller is an orchestrator pane's only repaint path, so input must not
  // disarm it.
  vi.advanceTimersByTime(1300);
  assert.equal(terminal.rows, 26, 'a still-blank pane must be repainted once typing pauses');
  assert.deepEqual(lastResizeCall(), [7, 120, 26]);
});

test('a repainted (now visible) pane is left alone even after the quiet window elapses', () => {
  const { terminal, repaint } = harness();
  repaint.schedule(7);
  repaint.noteInput();

  terminal.setContent('visible content');
  vi.advanceTimersByTime(3000);

  assert.deepEqual(terminal.resizes, [], 'a pane with visible content must never be resized');
});

test('without input the cycle still completes, so the repaint is not simply disabled', () => {
  const { terminal, repaint } = harness();
  repaint.schedule(7);

  vi.advanceTimersByTime(1300);
  assert.equal(terminal.rows, 26);
  vi.advanceTimersByTime(650);
  assert.equal(terminal.rows, 40, 'the hold must restore on its own when nobody types');
  assert.equal(fitCount, 1);
});

test('restore skips fit() when the container is momentarily unmeasurable, but still restores geometry', () => {
  let afterRestoreCalls = 0;
  const { terminal, repaint } = harness(
    () => ({ cols: 2, rows: 1 }),
    () => {
      afterRestoreCalls += 1;
    },
  );
  repaint.schedule(7);

  vi.advanceTimersByTime(1300);
  assert.equal(terminal.rows, 26, 'expected the shrink to be applied');
  vi.advanceTimersByTime(650);

  assert.equal(terminal.cols, 120);
  assert.equal(terminal.rows, 40, 'the hold must restore even though fit() was skipped');
  assert.equal(fitCount, 0, 'fit() must not run against an unusable proposal');
  assert.equal(afterRestoreCalls, 1, 'afterRestore still runs so dims are republished either way');
  assert.deepEqual(lastResizeCall(), [7, 120, 40]);
});
