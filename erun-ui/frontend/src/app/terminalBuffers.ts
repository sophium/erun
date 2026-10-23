import type { TerminalWriteData } from './model';
import type { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { cleanTerminalOutput } from './terminalStatus';

// xterm keeps only this many lines of scrollback, so retaining or replaying
// more than this budget is pure memory/parse cost xterm immediately
// discards on write. TERMINAL_SCROLLBACK must match the `scrollback` option
// TerminalController constructs the xterm instance with.
export const TERMINAL_SCROLLBACK = 5000;
// A little more than the scrollback (long lines wrap into several rows), but
// never more than a hard byte ceiling so a sparse-newline stream can't blow
// the cap. Both bound cost to O(scrollback), never O(total session history) --
// this is also the hard memory bound on a single session's retained buffer
// (documented at erun-docs/docs/desktop/terminals-and-editors.md).
export const MAX_RETAINED_LINES = TERMINAL_SCROLLBACK * 2;
export const MAX_RETAINED_BYTES = 2_000_000;

// Retention trims down to this fraction of the budget rather than to the
// budget itself, so a session that has filled its budget absorbs a quarter of
// a budget's worth of new output before the next trim. Trimming only back to
// the budget left the retained tail at it, and the very next chunk crossed it
// again: every chunk paid an O(retained) rescan. Three quarters of
// MAX_RETAINED_LINES is still above TERMINAL_SCROLLBACK, so a replay still
// fills xterm's scrollback.
export const RETAINED_TRIM_TARGET = 0.75;

// Exported so TerminalSessionRegistry can maintain a running per-session
// line/byte total incrementally (O(1) per append) instead of re-summing the
// whole retained array on every single output chunk -- see appendDisplayBuffer.
// indexOf, not for...of: iterating a Uint8Array goes through the typed-array
// iterator, which costs a length and detached check per byte in JSC.
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

// trimChunksWithinBudget keeps the newest chunks whose totals stay within the
// budget -- always at least the newest chunk, however large -- and reports the
// totals of what it kept, so a caller tracking running totals need not rescan.
// Returns the original array reference when everything fits.
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

// trimChunksToBudget keeps only the tail of a chunk array worth keeping --
// enough to fill xterm's scrollback, not the whole session history. Returns
// the original array reference when nothing needs trimming. Used to bound a
// cold-start replay of a session that has no snapshot yet.
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

// Strip terminal query *responses* that leak into the displayed output as
// gibberish: a tool inside the PTY sends a query (e.g. a cursor-position
// report), xterm answers on the PTY's stdin, and the shell echoes the answer
// back through the output stream. Stripping at the display boundary keeps the
// visible buffer clean without changing what the tools see. The bare-tail
// variants match what survives when readline eats the leading unprintable
// prefix and echoes the rest as plain text; tails anchor on a non-alphanumeric
// boundary so real prompt content (a `c` or `R` after a digit) is not mangled.
const TERMINAL_RESPONSE_PATTERNS: RegExp[] = [
  /\x1B\[\d+;\d+R/g,
  /\x1B\[\d+n/g,
  /\x1B\[[?>]?[\d;]+c/g,
  /(?:^|[^A-Za-z0-9])\d+(?:;\d+)+c(?![A-Za-z0-9])/g,
  /(?:^|[^A-Za-z0-9])(?:\d+(?:;\d+)+R)+(?![A-Za-z0-9])/g,
  /(?:\x1BP)?[01]\$r[^\x1B]*\x1B\\/g,
];

// Takes the chunk already decoded, so the output path decodes each chunk once.
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

// A spinner inside a subprocess (helm, kubectl, git over SSH, the deploy
// pipeline) can leak an unmatched cursor-hide outside the alternate screen,
// leaving the bash prompt with no visible cursor. The replay path tracks
// cursor-visibility and alt-screen state so it can re-emit a cursor-show at
// the prompt while leaving live TUIs (which sit in the alternate screen)
// untouched.
export interface CursorVisibilityState {
  altScreen: boolean;
  cursorHidden: boolean;
  // A DEC private-mode escape (e.g. `\x1B[?1049h`) can be split across PTY output
  // chunks. Carry any trailing incomplete fragment into the next scan so a split
  // alt-screen enter/exit (or cursor hide/show) is not silently missed — missing
  // it mis-routed a session switch to the trim path and blanked alt-screen TUIs.
  pendingEscape?: string;
}

export const SHOW_CURSOR_SEQUENCE = '\x1B[?25h';

const initialCursorVisibility: CursorVisibilityState = {
  altScreen: false,
  cursorHidden: false,
};

const DEC_PRIVATE_MODE = /\x1B\[\?([\d;]+)([lh])/g;
const ALT_SCREEN_MODES = new Set(['47', '1047', '1049']);
// A trailing fragment at the end of a chunk that could be the start of a DEC
// private-mode sequence still waiting for its terminator: `\x1B`, `\x1B[`,
// `\x1B[?`, or `\x1B[?12;` with no `l`/`h` yet. Carried into the next scan.
const TRAILING_DEC_FRAGMENT = /\x1B(?:\[(?:\?[\d;]*)?)?$/;

// applyDecPrivateMode folds one matched `\x1B[?<params><l|h>` into the running
// state: `?25` toggles cursor visibility, alt-screen modes toggle altScreen.
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
  // Prepend any incomplete escape fragment carried from the previous chunk so a
  // sequence split across chunk boundaries is reassembled before matching.
  const carried = prev.pendingEscape ?? '';
  const text = carried + decoded;
  const next = { altScreen: prev.altScreen, cursorHidden: prev.cursorHidden };
  for (const match of text.matchAll(DEC_PRIVATE_MODE)) {
    applyDecPrivateMode(next, match[1] ?? '', match[2] === 'h');
  }
  // Only an incomplete DEC tail is carried; a completed sequence matched above
  // ends in l/h and never matches this, so nothing is ever counted twice.
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

// The bytes to display and the same content as text, so a caller that also
// scans the displayed output need not decode it again.
export interface TerminalDisplayData {
  data: Uint8Array;
  text: string;
}

export function filterTerminalDisplay(data: Uint8Array, text: string): TerminalDisplayData {
  return stripTerminalResponses(data, text);
}
