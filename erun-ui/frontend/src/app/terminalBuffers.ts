import type { TerminalWriteData } from './model';
import type { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { cleanTerminalOutput } from './terminalStatus';

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

export function rebuildTerminalDisplayBuffer(
  sessions: TerminalSessionRegistry,
  sessionId: number,
): void {
  const chunks = sessions.sessionBuffer(sessionId);
  const displayBuffer: TerminalWriteData[] = [];
  for (const chunk of chunks) {
    displayBuffer.push(filterTerminalDisplayData(chunk));
  }
  const finalState = bufferCursorVisibility(displayBuffer);
  if (!finalState.altScreen && finalState.cursorHidden) {
    displayBuffer.push(SHOW_CURSOR_SEQUENCE);
  }
  sessions.replaceDisplayBuffer(sessionId, displayBuffer);
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

function stripTerminalResponses(input: Uint8Array): Uint8Array {
  const decoder = new TextDecoder();
  const text = decoder.decode(input);
  let cleaned = text;
  for (const pattern of TERMINAL_RESPONSE_PATTERNS) {
    cleaned = cleaned.replace(pattern, '');
  }
  if (cleaned === text) {
    return input;
  }
  return new TextEncoder().encode(cleaned);
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
}

export const SHOW_CURSOR_SEQUENCE = '\x1B[?25h';

const initialCursorVisibility: CursorVisibilityState = {
  altScreen: false,
  cursorHidden: false,
};

const DEC_PRIVATE_MODE = /\x1B\[\?([\d;]+)([lh])/g;
const ALT_SCREEN_MODES = new Set(['47', '1047', '1049']);

export function scanCursorVisibility(
  prev: CursorVisibilityState,
  data: TerminalWriteData,
): CursorVisibilityState {
  const text = typeof data === 'string' ? data : new TextDecoder().decode(data);
  if (!text.includes('\x1B[?')) {
    return prev;
  }
  let { altScreen, cursorHidden } = prev;
  for (const match of text.matchAll(DEC_PRIVATE_MODE)) {
    const set = match[2] === 'h';
    const params = match[1] ?? '';
    for (const param of params.split(';')) {
      if (param === '25') {
        cursorHidden = !set;
      } else if (ALT_SCREEN_MODES.has(param)) {
        altScreen = set;
      }
    }
  }
  if (altScreen === prev.altScreen && cursorHidden === prev.cursorHidden) {
    return prev;
  }
  return { altScreen, cursorHidden };
}

export function bufferCursorVisibility(buffer: TerminalWriteData[]): CursorVisibilityState {
  let state = initialCursorVisibility;
  for (const chunk of buffer) {
    state = scanCursorVisibility(state, chunk);
  }
  return state;
}

export function filterTerminalDisplayData(data: Uint8Array): TerminalWriteData {
  return stripTerminalResponses(data);
}
