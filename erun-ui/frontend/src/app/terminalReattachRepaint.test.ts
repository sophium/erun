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
  let content = '';
  return {
    cols,
    rows,
    resizes,
    // setContent lets a test simulate the program redrawing on its own, e.g.
    // while typing is still within the quiet window.
    setContent(next: string) {
      content = next;
    },
    // Empty by default: the state dtach leaves a reattached pane in, and
    // therefore the state the repaint cycle fires on.
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
  mock.timers.enable({ apis: ['setTimeout', 'setInterval', 'Date'] });
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
  // being edited is the corruption. Tick less than a full poller period (1300ms)
  // past the restore so this stays a test of the cancelled timeout, not of the
  // (legitimate, separately covered) next poll cycle once the quiet window
  // elapses.
  const afterInput = terminal.resizes.length;
  mock.timers.tick(700);
  assert.equal(terminal.resizes.length, afterInput, 'the cancelled hold must not fire');
  assert.equal(fitCount, 1, 'the cancelled hold must not re-fit again');
});

test('input defers the cycle through the quiet window but does not disarm it', () => {
  const { terminal, repaint } = harness();
  repaint.schedule(7);
  repaint.noteInput();

  // A tick landing inside the 1500ms quiet window must not resize a pane the
  // operator is actively typing into.
  mock.timers.tick(1300);
  assert.deepEqual(terminal.resizes, [], 'a tick within the quiet window must not resize');

  // Once the quiet window elapses with the screen still blank, the poller must
  // still repaint it. This is the only repaint path left for an orchestrator
  // pane (#1332's Go-side WINCH nudge is dead code for that session kind), so
  // permanently disarming on the first keystroke left the pane blank for the
  // rest of the session -- exactly the defect behind the operator's blind,
  // concatenated retype (#1330 follow-up).
  mock.timers.tick(1300);
  assert.equal(terminal.rows, 26, 'a still-blank pane must be repainted once typing pauses');
  assert.deepEqual(lastResizeCall(), [7, 120, 26]);
});

test('a repainted (now visible) pane is left alone even after the quiet window elapses', () => {
  const { terminal, repaint } = harness();
  repaint.schedule(7);
  repaint.noteInput();

  // The screen becomes visible while the operator is still within the quiet
  // window -- e.g. the program redrew on its own after the keystroke.
  terminal.setContent('visible content');
  mock.timers.tick(3000);

  assert.deepEqual(terminal.resizes, [], 'a pane with visible content must never be resized');
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

test('restore skips fit() when the container is momentarily unmeasurable, but still restores geometry', () => {
  let afterRestoreCalls = 0;
  const { terminal, repaint } = harness(
    () => ({ cols: 2, rows: 1 }), // the container mid-transition FitAddon.fit() would otherwise apply
    () => {
      afterRestoreCalls += 1;
    },
  );
  repaint.schedule(7);

  mock.timers.tick(1300);
  assert.equal(terminal.rows, 26, 'expected the shrink to be applied');
  mock.timers.tick(650);

  // term.resize(cols, rows) already restores the pre-shrink geometry directly,
  // independent of fit(); the guard only stops the *extra* re-measure from
  // clobbering it with a bad proposal.
  assert.equal(terminal.cols, 120);
  assert.equal(terminal.rows, 40, 'the hold must restore even though fit() was skipped');
  assert.equal(fitCount, 0, 'fit() must not run against an unusable proposal');
  assert.equal(afterRestoreCalls, 1, 'afterRestore still runs so dims are republished either way');
  assert.deepEqual(lastResizeCall(), [7, 120, 40]);
});
