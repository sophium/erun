import assert from 'node:assert/strict';

import { test } from 'vitest';

import {
  classifyTerminalClipboardKey,
  type ClipboardPlatform,
  parseOscClipboardWrite,
  terminalCopyOutcome,
} from './clipboard';

interface KeyInit {
  key: string;
  type?: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  shiftKey?: boolean;
  altKey?: boolean;
}

function keyEvent(init: KeyInit): KeyboardEvent {
  return {
    type: 'keydown',
    ctrlKey: false,
    metaKey: false,
    shiftKey: false,
    altKey: false,
    ...init,
  } as unknown as KeyboardEvent;
}

function classify(platform: ClipboardPlatform, init: KeyInit): string {
  return classifyTerminalClipboardKey(keyEvent(init), platform);
}

test('macOS copies with Cmd+C', () => {
  assert.equal(classify('mac', { key: 'c', metaKey: true }), 'copy');
  assert.equal(classify('mac', { key: 'C', metaKey: true }), 'copy');
});

test('macOS leaves Ctrl+C to the session so it can still interrupt', () => {
  assert.equal(classify('mac', { key: 'c', ctrlKey: true }), 'none');
  assert.equal(classify('mac', { key: 'c', ctrlKey: true, shiftKey: true }), 'none');
});

test('macOS leaves paste chords to the native paste event', () => {
  assert.equal(classify('mac', { key: 'v', metaKey: true }), 'none');
  assert.equal(classify('mac', { key: 'v', ctrlKey: true }), 'none');
});

test('Windows/Linux paste chords are unchanged', () => {
  assert.equal(classify('other', { key: 'v', ctrlKey: true }), 'paste');
  assert.equal(classify('other', { key: 'v', ctrlKey: true, shiftKey: true }), 'paste');
  assert.equal(classify('other', { key: 'Insert', shiftKey: true }), 'paste');
});

test('Windows/Linux copy chords are unchanged', () => {
  assert.equal(classify('other', { key: 'c', ctrlKey: true }), 'copy');
  assert.equal(classify('other', { key: 'c', ctrlKey: true, shiftKey: true }), 'copy');
});

test('Windows/Linux ignore Alt-modified copy and paste chords', () => {
  assert.equal(classify('other', { key: 'c', ctrlKey: true, altKey: true }), 'none');
  assert.equal(classify('other', { key: 'v', ctrlKey: true, altKey: true }), 'none');
});

test('the meta modifier is part of the chord on both platforms', () => {
  assert.equal(classify('other', { key: 'c', metaKey: true }), 'none');
  assert.equal(classify('other', { key: 'v', metaKey: true }), 'none');
  assert.equal(classify('mac', { key: 'c' }), 'none');
  assert.equal(classify('mac', { key: 'v' }), 'none');
});

test('only keydown carries a clipboard intent', () => {
  assert.equal(classify('mac', { key: 'c', metaKey: true, type: 'keyup' }), 'none');
  assert.equal(classify('other', { key: 'c', ctrlKey: true, type: 'keyup' }), 'none');
  assert.equal(classify('other', { key: 'v', ctrlKey: true, type: 'keyup' }), 'none');
});

test('a copy chord with no selection falls through to the session', () => {
  assert.equal(terminalCopyOutcome(keyEvent({ key: 'c', ctrlKey: true }), false), 'fallthrough');
  assert.equal(terminalCopyOutcome(keyEvent({ key: 'c', metaKey: true }), false), 'fallthrough');
});

test('a copy chord with a selection copies it', () => {
  assert.equal(terminalCopyOutcome(keyEvent({ key: 'c', ctrlKey: true }), true), 'copy');
  assert.equal(terminalCopyOutcome(keyEvent({ key: 'c', metaKey: true }), true), 'copy');
});

test('Ctrl+Shift+C with no selection is consumed rather than sent as ^C', () => {
  const event = keyEvent({ key: 'c', ctrlKey: true, shiftKey: true });
  assert.equal(terminalCopyOutcome(event, false), 'swallow');
});

// OSC 52 input comes from the pod and is untrusted.

function osc52(selection: string, text: string): string {
  return `${selection};${Buffer.from(text, 'utf8').toString('base64')}`;
}

test('an OSC 52 clipboard write decodes to its text', () => {
  assert.equal(
    parseOscClipboardWrite(osc52('c', 'https://example.test/auth?code=abc')),
    'https://example.test/auth?code=abc',
  );
});

test('an OSC 52 write decodes multi-byte text as UTF-8', () => {
  assert.equal(parseOscClipboardWrite(osc52('c', 'héllo → wörld')), 'héllo → wörld');
});

test('an OSC 52 write with no explicit selection targets the clipboard', () => {
  assert.equal(parseOscClipboardWrite(osc52('', 'plain')), 'plain');
});

test('an OSC 52 write aimed only at the primary selection or a cut buffer is ignored', () => {
  assert.equal(parseOscClipboardWrite(osc52('p', 'mouse selection')), null);
  assert.equal(parseOscClipboardWrite(osc52('0', 'cut buffer')), null);
  assert.equal(parseOscClipboardWrite(osc52('pc', 'both')), 'both');
});

test('an OSC 52 read request yields nothing to write', () => {
  assert.equal(parseOscClipboardWrite('c;?'), null);
  assert.equal(parseOscClipboardWrite(';?'), null);
});

test('a malformed OSC 52 payload is dropped rather than thrown', () => {
  assert.equal(parseOscClipboardWrite('c;not!valid!base64'), null);
  assert.equal(parseOscClipboardWrite('c;'), null);
  assert.equal(parseOscClipboardWrite('no-separator'), null);
  assert.equal(parseOscClipboardWrite(''), null);
});

test('an oversized OSC 52 payload is refused before it is decoded', () => {
  assert.equal(parseOscClipboardWrite(osc52('c', 'x'.repeat(128 * 1024))), null);
  assert.equal(parseOscClipboardWrite(osc52('c', 'x'.repeat(4096)))?.length, 4096);
});
