import assert from 'node:assert/strict';

import { test } from 'vitest';

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

// A capture can come back empty from a session that was never painted -- a
// switch away dispatched before the outgoing session's writes have parsed.
// That is not a screen: recording it would blank the session's remembered
// screen and drop the buffer that was the only other copy of it.
test('an empty capture neither becomes the snapshot nor clears the buffer', () => {
  const sessions = new TerminalSessionRegistry();
  sessions.appendDisplayBuffer(1, 'line 1\n');

  sessions.captureSnapshot(1, '');

  assert.equal(sessions.snapshot(1), undefined);
  assert.deepEqual(sessions.displayBuffer(1), ['line 1\n']);
});

test('an empty capture leaves an existing snapshot alone', () => {
  const sessions = new TerminalSessionRegistry();
  sessions.captureSnapshot(1, 'SERIALIZED_SCREEN');
  sessions.appendDisplayBuffer(1, 'line 2\n');

  sessions.captureSnapshot(1, '');

  assert.equal(sessions.snapshot(1), 'SERIALIZED_SCREEN');
  assert.deepEqual(sessions.displayBuffer(1), ['line 2\n']);
});

// A switch is dispatched while the outgoing session's last output is still in
// xterm's write queue, so lines for that session keep arriving after the switch
// but before the capture that clears the buffer. They are not on the screen the
// snapshot carries, so they have to survive it -- dropping them loses output for
// a session nobody re-activates until the next switch back.
test('captureSnapshot keeps output that arrived after the switch was dispatched', () => {
  const sessions = new TerminalSessionRegistry();
  sessions.appendDisplayBuffer(1, 'rendered\n');
  // The session's append count at dispatch: where the post-dispatch output
  // begins, however the retained array moves in between.
  const atDispatch = sessions.displayAppendedCount(1);
  sessions.appendDisplayBuffer(1, 'still queued\n');

  sessions.captureSnapshot(1, 'SERIALIZED_SCREEN', atDispatch);

  assert.equal(sessions.snapshot(1), 'SERIALIZED_SCREEN');
  assert.deepEqual(sessions.displayBuffer(1), ['still queued\n']);
});

// A session at its retention budget trims the head of its retained array on
// every later append -- which is the standing state of a long-running build
// log, and the one where a boundary taken as a length points at output the
// snapshot does not carry, or past the end of an array that got shorter.
test('captureSnapshot keeps that output across a trim of the head', () => {
  const sessions = new TerminalSessionRegistry();
  const chunk = 'x'.repeat(1000);
  for (let i = 0; i < Math.ceil(MAX_RETAINED_BYTES / chunk.length) + 10; i++) {
    sessions.appendDisplayBuffer(3, chunk);
  }
  const atDispatch = sessions.displayAppendedCount(3);
  const before = sessions.displayBuffer(3).length;
  // Big enough to cross the budget again on its own, which is what makes the
  // append trim the head -- the state this case exists to reach.
  const postDispatch = `post-dispatch\n${'y'.repeat(2000)}`;
  sessions.appendDisplayBuffer(3, postDispatch);
  assert.ok(
    sessions.displayBuffer(3).length < before,
    'the append under test must trim the retained head for this case to mean anything',
  );

  sessions.captureSnapshot(3, 'SERIALIZED_SCREEN', atDispatch);

  assert.equal(sessions.snapshot(3), 'SERIALIZED_SCREEN');
  assert.deepEqual(sessions.displayBuffer(3), [postDispatch]);
});

test('snapshots and buffers are independent per session', () => {
  const sessions = new TerminalSessionRegistry();
  sessions.captureSnapshot(1, 'ONE');
  sessions.appendDisplayBuffer(2, 'two\n');
  assert.equal(sessions.snapshot(1), 'ONE');
  assert.equal(sessions.snapshot(2), undefined);
  assert.deepEqual(sessions.displayBuffer(2), ['two\n']);
});
