import assert from 'node:assert/strict';
import { test } from 'node:test';

import { bufferCursorVisibility } from './terminalBuffers';

const ALT_ENTER = '\x1B[?1049h';

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
