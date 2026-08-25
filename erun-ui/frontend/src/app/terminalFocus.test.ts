import assert from 'node:assert/strict';
import { afterEach, beforeEach, mock, test } from 'node:test';

import { scheduleTerminalFocus } from './terminalFocus';

// scheduleTerminalFocus touches window only inside its body, never at module
// scope, so a per-test shim is enough to drive the real function.
let rafQueue: (() => void)[] = [];

function installWindow(): void {
  const shim = {
    setTimeout: (handler: () => void, ms?: number) => setTimeout(handler, ms) as unknown as number,
    clearTimeout: (id: number) => {
      clearTimeout(id);
    },
    requestAnimationFrame: (handler: () => void) => {
      rafQueue.push(handler);
      return rafQueue.length;
    },
  };
  (globalThis as unknown as { window: unknown }).window = shim;
}

function flushRaf(): void {
  const queued = rafQueue;
  rafQueue = [];
  for (const handler of queued) {
    handler();
  }
}

beforeEach(() => {
  rafQueue = [];
  installWindow();
  mock.timers.enable({ apis: ['setTimeout'] });
});

afterEach(() => {
  mock.timers.reset();
});

function harness(windowIsActive: () => boolean) {
  let focusCount = 0;
  scheduleTerminalFocus({
    getTerminal: () => ({
      focus: () => {
        focusCount += 1;
      },
    }),
    windowIsActive,
  });
  return {
    count: () => focusCount,
  };
}

test('focus is restored when the window is already active', () => {
  const h = harness(() => true);
  mock.timers.tick(0);
  flushRaf();
  mock.timers.tick(80);
  assert.equal(h.count(), 3, 'all three attempts should focus an active window');
});

// #1338: a tab respawn fires whenever a session drops -- on every env stop,
// idle-stop and pod replacement -- and it calls this. Focusing a DOM element in
// a Wails webview raises the native window, so an unguarded restore pulled the
// desktop in front of whatever the operator was actually using. This must fail
// against the unguarded `this.terminal?.focus()` it replaces.
test('focus is never taken while another application is frontmost', () => {
  const h = harness(() => false);
  mock.timers.tick(0);
  flushRaf();
  mock.timers.tick(80);
  assert.equal(h.count(), 0, 'an inactive window must never pull focus');
});

// The later attempts exist to land after a re-render, so the guard has to be
// re-read each time rather than sampled once when the work was scheduled.
test('the guard is re-checked, so focus leaving the window mid-sequence stops it', () => {
  let active = true;
  const h = harness(() => active);
  mock.timers.tick(0);
  assert.equal(h.count(), 1);
  active = false;
  flushRaf();
  mock.timers.tick(80);
  assert.equal(h.count(), 1, 'attempts after focus left the window must stand down');
});
