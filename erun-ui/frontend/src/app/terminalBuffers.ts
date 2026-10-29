import type { TerminalWriteData } from './model';
import type { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { cleanTerminalOutput } from './terminalStatus';

// Must match the `scrollback` option TerminalController gives xterm; more is
// discarded on write.
export const TERMINAL_SCROLLBACK = 5000;
// Lines wrap into several rows, so retain more than the scrollback, under a
// hard byte ceiling for sparse-newline streams.
export const MAX_RETAINED_LINES = TERMINAL_SCROLLBACK * 2;
export const MAX_RETAINED_BYTES = 2_000_000;

// Trimming below the budget leaves headroom, so each new chunk does not
// trigger another O(retained) rescan.
export const RETAINED_TRIM_TARGET = 0.75;

// indexOf, not for...of: Uint8Array iteration costs a check per byte in JSC.
export function countNewlines(chunk: TerminalWriteData): number {
  let count = 0;
  let from = 0;
  if (typeof chunk === 'string') {
    for (let at = chunk.indexOf('\n'); at !== -1; at = chunk.indexOf('\n', from)) {
      count++;
      from = at + 1;
    }
    return count;
  }
  for (let at = chunk.indexOf(10); at !== -1; at = chunk.indexOf(10, from)) {
    count++;
    from = at + 1;
  }
  return count;
}

export interface TrimmedChunks<T extends TerminalWriteData> {
  chunks: T[];
  lines: number;
  bytes: number;
}

// trimChunksWithinBudget always keeps at least the newest chunk and returns the
// original array when everything fits.
export function trimChunksWithinBudget<T extends TerminalWriteData>(
  chunks: T[],
  maxLines: number,
  maxBytes: number,
): TrimmedChunks<T> {
  let lines = 0;
  let bytes = 0;
  let start = chunks.length;
  for (let i = chunks.length - 1; i >= 0; i--) {
    const chunk = chunks[i];
    if (chunk === undefined) continue;
    const chunkLines = countNewlines(chunk);
    const withinBudget = lines + chunkLines <= maxLines && bytes + chunk.length <= maxBytes;
    if (!withinBudget && start < chunks.length) break;
    lines += chunkLines;
    bytes += chunk.length;
    start = i;
  }
  return { chunks: start === 0 ? chunks : chunks.slice(start), lines, bytes };
}

// trimChunksToBudget returns the original array when nothing needs trimming.
export function trimChunksToBudget(
  chunks: TerminalWriteData[],
  maxLines: number,
  maxBytes: number,
): TerminalWriteData[] {
  return trimChunksWithinBudget(chunks, maxLines, maxBytes).chunks;
}

export function failedTerminalOutput(
  sessions: TerminalSessionRegistry,
  sessionId: number,
  fallback: string,
): string {
  const chunks = sessions.sessionBuffer(sessionId);
  const decoder = new TextDecoder();
  const output =
    chunks.map((chunk) => decoder.decode(chunk, { stream: true })).join('') + decoder.decode();
  return cleanTerminalOutput(output) || fallback;
}

// Strips terminal query responses the shell echoes back into the output. Bare
// tails cover readline eating the escape prefix; they anchor on a
// non-alphanumeric boundary so prompt text is not mangled.
const TERMINAL_RESPONSE_PATTERNS: RegExp[] = [
  /\x1B\[\d+;\d+R/g,
  /\x1B\[\d+n/g,
  /\x1B\[[?>]?[\d;]+c/g,
  /(?:^|[^A-Za-z0-9])\d+(?:;\d+)+c(?![A-Za-z0-9])/g,
  /(?:^|[^A-Za-z0-9])(?:\d+(?:;\d+)+R)+(?![A-Za-z0-9])/g,
  /(?:\x1BP)?[01]\$r[^\x1B]*\x1B\\/g,
];

function stripTerminalResponses(input: Uint8Array, text: string): TerminalDisplayData {
  let cleaned = text;
  for (const pattern of TERMINAL_RESPONSE_PATTERNS) {
    cleaned = cleaned.replace(pattern, '');
  }
  if (cleaned === text) {
    return { data: input, text };
  }
  return { data: new TextEncoder().encode(cleaned), text: cleaned };
}

// Subprocess spinners can leak a cursor-hide outside the alt screen; replay
// re-emits cursor-show at the prompt but leaves alt-screen TUIs alone.
export interface CursorVisibilityState {
  altScreen: boolean;
  cursorHidden: boolean;
  // Incomplete escape fragment carried into the next chunk's scan.
  pendingEscape?: string;
}

export const SHOW_CURSOR_SEQUENCE = '\x1B[?25h';

const initialCursorVisibility: CursorVisibilityState = {
  altScreen: false,
  cursorHidden: false,
};

const DEC_PRIVATE_MODE = /\x1B\[\?([\d;]+)([lh])/g;
const ALT_SCREEN_MODES = new Set(['47', '1047', '1049']);
const TRAILING_DEC_FRAGMENT = /\x1B(?:\[(?:\?[\d;]*)?)?$/;

function applyDecPrivateMode(
  state: { altScreen: boolean; cursorHidden: boolean },
  params: string,
  set: boolean,
): void {
  for (const param of params.split(';')) {
    if (param === '25') {
      state.cursorHidden = !set;
    } else if (ALT_SCREEN_MODES.has(param)) {
      state.altScreen = set;
    }
  }
}

export function scanCursorVisibility(
  prev: CursorVisibilityState,
  data: TerminalWriteData,
): CursorVisibilityState {
  const decoded = typeof data === 'string' ? data : new TextDecoder().decode(data);
  const carried = prev.pendingEscape ?? '';
  const text = carried + decoded;
  const next = { altScreen: prev.altScreen, cursorHidden: prev.cursorHidden };
  for (const match of text.matchAll(DEC_PRIVATE_MODE)) {
    applyDecPrivateMode(next, match[1] ?? '', match[2] === 'h');
  }
  // A completed sequence ends in l/h and never matches, so nothing counts twice.
  const tailMatch = TRAILING_DEC_FRAGMENT.exec(text);
  const pendingEscape = tailMatch ? tailMatch[0] : '';
  if (
    next.altScreen === prev.altScreen &&
    next.cursorHidden === prev.cursorHidden &&
    pendingEscape === carried
  ) {
    return prev;
  }
  return { altScreen: next.altScreen, cursorHidden: next.cursorHidden, pendingEscape };
}

export function bufferCursorVisibility(buffer: TerminalWriteData[]): CursorVisibilityState {
  let state = initialCursorVisibility;
  for (const chunk of buffer) {
    state = scanCursorVisibility(state, chunk);
  }
  return state;
}

export interface TerminalDisplayData {
  data: Uint8Array;
  text: string;
}

export function filterTerminalDisplay(data: Uint8Array, text: string): TerminalDisplayData {
  return stripTerminalResponses(data, text);
}
