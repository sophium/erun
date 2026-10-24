import assert from 'node:assert/strict';

import { afterEach, beforeEach, test, vi } from 'vitest';

import { scheduleTerminalFocus } from './terminalFocus';

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
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
});

afterEach(() => {
  vi.useRealTimers();
});

function harness(windowIsActive: () => boolean, focusIsFree: () => boolean = () => true) {
  let focusCount = 0;
  scheduleTerminalFocus({
    getTerminal: () => ({
      focus: () => {
        focusCount += 1;
      },
    }),
    windowIsActive,
    focusIsFree,
  });
  return {
    count: () => focusCount,
  };
}

test('focus is restored when the window is already active', () => {
  const h = harness(() => true);
  vi.advanceTimersByTime(0);
  flushRaf();
  vi.advanceTimersByTime(80);
  assert.equal(h.count(), 3, 'all three attempts should focus an active window');
});

test('focus is never taken while another application is frontmost', () => {
  const h = harness(() => false);
  vi.advanceTimersByTime(0);
  flushRaf();
  vi.advanceTimersByTime(80);
  assert.equal(h.count(), 0, 'an inactive window must never pull focus');
});

test('the guard is re-checked, so focus leaving the window mid-sequence stops it', () => {
  let active = true;
  const h = harness(() => active);
  vi.advanceTimersByTime(0);
  assert.equal(h.count(), 1);
  active = false;
  flushRaf();
  vi.advanceTimersByTime(80);
  assert.equal(h.count(), 1, 'attempts after focus left the window must stand down');
});

test('focus is never taken off a control the user deliberately focused', () => {
  const h = harness(
    () => true,
    () => false,
  );
  vi.advanceTimersByTime(0);
  flushRaf();
  vi.advanceTimersByTime(80);
  assert.equal(h.count(), 0, 'a control holding focus must not be overruled');
});

test('a restore still happens when nothing else holds focus', () => {
  let free = true;
  const h = harness(
    () => true,
    () => free,
  );
  vi.advanceTimersByTime(0);
  assert.equal(h.count(), 1, 'a dialog close leaves focus free, so the restore must run');
  free = false;
  flushRaf();
  vi.advanceTimersByTime(80);
  assert.equal(h.count(), 1, 'later attempts must stand down once a control takes focus');
});
