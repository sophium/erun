import assert from 'node:assert/strict';
import { test } from 'node:test';

import { bufferCursorVisibility, trimChunksToBudget } from './terminalBuffers';

const ALT_ENTER = '\x1B[?1049h';

test('trimChunksToBudget keeps everything under the line budget', () => {
  const chunks = ['a\n', 'b\n', 'c\n'];
  assert.equal(trimChunksToBudget(chunks, 10, 1_000_000), chunks);
});

test('trimChunksToBudget keeps only the tail once the line budget is exceeded', () => {
  const chunks = ['1\n', '2\n', '3\n', '4\n', '5\n'];
  // Budget of 2 lines: walk from the tail, stop once 2 newlines are counted.
  const trimmed = trimChunksToBudget(chunks, 2, 1_000_000);
  assert.deepEqual(trimmed, ['4\n', '5\n']);
});

test('trimChunksToBudget keeps only the tail once the byte budget is exceeded', () => {
  const chunks = ['aaaa', 'bbbb', 'cccc'];
  const trimmed = trimChunksToBudget(chunks, 1_000_000, 5);
  assert.deepEqual(trimmed, ['bbbb', 'cccc']);
});

test('trimChunksToBudget returns the original array reference when nothing is trimmed', () => {
  const chunks = ['x\n'];
  assert.strictEqual(trimChunksToBudget(chunks, 100, 100), chunks);
});

test('detects alt-screen enter in a single chunk', () => {
  assert.equal(bufferCursorVisibility([ALT_ENTER]).altScreen, true);
});

// The regression that blanked the Claude (alt-screen) tab: a PTY can split the
// escape sequence at ANY byte boundary, and a per-chunk scan missed it, leaving
// altScreen=false so the switch took the trim path onto a reset screen.
test('detects alt-screen enter split at every chunk boundary', () => {
  for (let cut = 1; cut < ALT_ENTER.length; cut++) {
    const head = ALT_ENTER.slice(0, cut);
    const tail = ALT_ENTER.slice(cut);
    const state = bufferCursorVisibility([head, tail]);
    assert.equal(
      state.altScreen,
      true,
      `split after ${String(cut)} chars (${JSON.stringify(head)} | ${JSON.stringify(tail)}) should still detect alt-screen`,
    );
  }
});

test('alt-screen enter split across three chunks with content between', () => {
  const state = bufferCursorVisibility(['prompt$ ', '\x1B[?10', '49h', 'app frame']);
  assert.equal(state.altScreen, true);
});

test('alt-screen exit split across chunks is detected', () => {
  const state = bufferCursorVisibility([ALT_ENTER, '\x1B[?1049', 'l']);
  assert.equal(state.altScreen, false);
});

test('a trailing bare CSI (SGR) split across chunks is not misread as alt-screen', () => {
  const state = bufferCursorVisibility([ALT_ENTER, '\x1B[', '0m done']);
  assert.equal(state.altScreen, true); // still in alt-screen; the SGR must not flip it
});

test('cursor hide/show split across chunks is tracked', () => {
  assert.equal(bufferCursorVisibility(['\x1B[?25', 'l']).cursorHidden, true);
  assert.equal(bufferCursorVisibility(['\x1B[?25l', '\x1B[?25', 'h']).cursorHidden, false);
});

test('Uint8Array chunks split mid-sequence are detected', () => {
  const bytes = new TextEncoder().encode(ALT_ENTER);
  const state = bufferCursorVisibility([bytes.slice(0, 4), bytes.slice(4)]);
  assert.equal(state.altScreen, true);
});
