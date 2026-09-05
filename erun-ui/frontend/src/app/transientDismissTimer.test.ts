import assert from 'node:assert/strict';
import { afterEach, beforeEach, mock, test } from 'node:test';

import { scheduleTransientDismiss } from './transientDismissTimer';

// scheduleTransientDismiss touches window/document only inside its body,
// never at module scope, so a per-test shim is enough to drive the real
// function against mocked timers.
let listeners: Record<string, (() => void)[]> = {};
let focused = true;

function installWindow(): void {
  listeners = { blur: [], focus: [] };
  const windowShim = {
    // Unqualified globals, resolved at call time, so mock.timers is what runs.
    setTimeout: (handler: () => void, ms?: number) => setTimeout(handler, ms) as unknown as number,
    clearTimeout: (id: number) => {
      clearTimeout(id);
    },
    addEventListener: (type: string, handler: () => void) => {
      (listeners[type] ??= []).push(handler);
    },
    removeEventListener: (type: string, handler: () => void) => {
      listeners[type] = (listeners[type] ?? []).filter((h) => h !== handler);
    },
  };
  const documentShim = {
    hasFocus: () => focused,
  };
  (globalThis as unknown as { window: unknown }).window = windowShim;
  (globalThis as unknown as { document: unknown }).document = documentShim;
}

function fire(type: 'blur' | 'focus'): void {
  for (const handler of listeners[type] ?? []) {
    handler();
  }
}

beforeEach(() => {
  focused = true;
  installWindow();
  mock.timers.enable({ apis: ['setTimeout', 'Date'] });
});

afterEach(() => {
  mock.timers.reset();
});

test('runs onDismiss once the duration elapses while the window stays focused', () => {
  let dismissed = 0;
  scheduleTransientDismiss(3200, () => {
    dismissed += 1;
  });
  mock.timers.tick(3199);
  assert.equal(dismissed, 0, 'must not fire before its own duration');
  mock.timers.tick(1);
  assert.equal(dismissed, 1);
});

// The whole point of this module: a report that popped while the operator
// was in another application must not have silently ticked down to "read".
test('pauses while the window is blurred and only resumes counting down after focus returns', () => {
  let dismissed = 0;
  scheduleTransientDismiss(3200, () => {
    dismissed += 1;
  });
  mock.timers.tick(1000);
  focused = false;
  fire('blur');
  // Far past the original duration -- none of this time may count.
  mock.timers.tick(60_000);
  assert.equal(dismissed, 0, 'time spent blurred must never count toward the countdown');

  focused = true;
  fire('focus');
  mock.timers.tick(2199);
  assert.equal(dismissed, 0, 'only the remaining 2200ms from before the blur should be left');
  mock.timers.tick(1);
  assert.equal(dismissed, 1);
});

test('never starts the countdown at all if the window begins unfocused', () => {
  focused = false;
  let dismissed = 0;
  scheduleTransientDismiss(3200, () => {
    dismissed += 1;
  });
  mock.timers.tick(60_000);
  assert.equal(dismissed, 0, 'an unfocused window must never start the clock');

  focused = true;
  fire('focus');
  mock.timers.tick(3199);
  assert.equal(dismissed, 0);
  mock.timers.tick(1);
  assert.equal(dismissed, 1);
});

test('the returned cancel function stops it, even mid-countdown, and blur/focus no longer reschedule it', () => {
  let dismissed = 0;
  const cancel = scheduleTransientDismiss(3200, () => {
    dismissed += 1;
  });
  mock.timers.tick(1000);
  cancel();
  mock.timers.tick(60_000);
  assert.equal(dismissed, 0, 'a cancelled countdown must never fire');

  focused = false;
  fire('blur');
  focused = true;
  fire('focus');
  mock.timers.tick(60_000);
  assert.equal(dismissed, 0, 'a cancelled countdown must not be revived by focus churn');
});
