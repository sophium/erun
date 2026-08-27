import assert from 'node:assert/strict';
import { test } from 'node:test';

import { MAX_RETAINED_BYTES, MAX_RETAINED_LINES } from './terminalBuffers';
import { TerminalSessionRegistry } from './TerminalSessionRegistry';

test('appendDisplayBuffer retains everything under the budget', () => {
  const sessions = new TerminalSessionRegistry();
  sessions.appendDisplayBuffer(1, 'a\n');
  sessions.appendDisplayBuffer(1, 'b\n');
  assert.deepEqual(sessions.displayBuffer(1), ['a\n', 'b\n']);
});

// The regression #1322 exists to prevent: a long-running (or background,
// never-viewed) session must not grow its retained buffer without limit.
test('appendDisplayBuffer bounds retention once the line budget is exceeded', () => {
  const sessions = new TerminalSessionRegistry();
  const totalLines = MAX_RETAINED_LINES + 500;
  for (let i = 0; i < totalLines; i++) {
    sessions.appendDisplayBuffer(1, `line ${String(i)}\n`);
  }
  const retained = sessions.displayBuffer(1).map(String);
  let lines = 0;
  for (const chunk of retained) {
    lines += (chunk.match(/\n/g) ?? []).length;
  }
  assert.ok(
    lines <= MAX_RETAINED_LINES,
    `expected <= ${String(MAX_RETAINED_LINES)} lines, got ${String(lines)}`,
  );
  // The tail survives, not the head.
  assert.equal(retained[retained.length - 1], `line ${String(totalLines - 1)}\n`);
  assert.notEqual(retained[0], 'line 0\n');
});

test('appendDisplayBuffer bounds retention once the byte budget is exceeded', () => {
  const sessions = new TerminalSessionRegistry();
  const chunk = 'x'.repeat(1000);
  const chunkCount = Math.ceil(MAX_RETAINED_BYTES / chunk.length) + 10;
  for (let i = 0; i < chunkCount; i++) {
    sessions.appendDisplayBuffer(2, chunk);
  }
  const totalBytes = sessions.displayBuffer(2).reduce((sum, c) => sum + c.length, 0);
  assert.ok(
    totalBytes <= MAX_RETAINED_BYTES,
    `expected <= ${String(MAX_RETAINED_BYTES)} bytes, got ${String(totalBytes)}`,
  );
});

test('a session has no snapshot until one is captured', () => {
  const sessions = new TerminalSessionRegistry();
  assert.equal(sessions.snapshot(1), undefined);
});

// This is the mechanism #1322's fix relies on: capturing a snapshot clears the
// buffer, so a later switch back replays only the delta since, not the whole
// session history.
test('captureSnapshot records the snapshot and clears the display buffer', () => {
  const sessions = new TerminalSessionRegistry();
  sessions.appendDisplayBuffer(1, 'line 1\n');
  sessions.appendDisplayBuffer(1, 'line 2\n');

  sessions.captureSnapshot(1, 'SERIALIZED_SCREEN');

  assert.equal(sessions.snapshot(1), 'SERIALIZED_SCREEN');
  assert.deepEqual(sessions.displayBuffer(1), []);

  // Output that arrives after the snapshot is the delta a future switch back
  // needs to replay on top of it -- a screen-sized payload, not the log.
  sessions.appendDisplayBuffer(1, 'line 3\n');
  assert.deepEqual(sessions.displayBuffer(1), ['line 3\n']);
  assert.equal(sessions.snapshot(1), 'SERIALIZED_SCREEN');
});

test('snapshots and buffers are independent per session', () => {
  const sessions = new TerminalSessionRegistry();
  sessions.captureSnapshot(1, 'ONE');
  sessions.appendDisplayBuffer(2, 'two\n');
  assert.equal(sessions.snapshot(1), 'ONE');
  assert.equal(sessions.snapshot(2), undefined);
  assert.deepEqual(sessions.displayBuffer(2), ['two\n']);
});
