import type { TerminalWriteData } from './model';
import type { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { cleanTerminalOutput, interactivePromptIndex } from './terminalStatus';

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
  sessions.clearDebugFilter(sessionId);
  const chunks = sessions.sessionBuffer(sessionId);
  const displayBuffer: TerminalWriteData[] = [];
  for (const chunk of chunks) {
    const displayData = filterTerminalDisplayData(sessions, sessionId, chunk);
    if (displayData) {
      displayBuffer.push(displayData);
    }
  }
  const finalState = bufferCursorVisibility(displayBuffer);
  if (!finalState.altScreen && finalState.cursorHidden) {
    displayBuffer.push(SHOW_CURSOR_SEQUENCE);
  }
  sessions.replaceDisplayBuffer(sessionId, displayBuffer);
}

// Strip xterm CSI query *responses* that leak into the displayed output.
// A tool inside the PTY sends a query (e.g. CPR `\x1b[6n`); xterm answers
// via onData; the answer lands in the PTY's stdin, the shell echoes it
// as if the user typed it, and the echo comes back through the output
// stream as visible gibberish like `^[[51;1R`. Stripping the response
// patterns at the display boundary keeps the visible buffer clean
// without changing what the tools see. OSC 10/11/12 are not in this
// list because terminalQueryResponses no longer answers those queries
// (see the comment in that file).
//
// Each DA pattern covers both the full ESC-prefixed response *and* the
// bare `<n>;<n>c` tail that survives when bash's readline consumes the
// leading `\x1b[` and `\x1b[?` as an unknown function-key sequence,
// leaving just the digits + semicolons + trailing `c` echoed to screen
// as plain text. The tail pattern is anchored on a `c` that is
// immediately preceded by a digit so legitimate prompt content
// containing a `c` is not mangled (a bash prompt ending in `git:(main)
// $` is safe; a string like `2c` after a number is not).
const TERMINAL_RESPONSE_PATTERNS: RegExp[] = [
  // CSI Cursor Position Report response: ESC [ row ; col R
  /\x1B\[\d+;\d+R/g,
  // CSI Device Status Report response: ESC [ <n> n  (e.g. 0n)
  /\x1B\[\d+n/g,
  // Primary Device Attributes / Secondary Device Attributes responses
  // (CSI ? <params> c, CSI > <params> c, CSI <params> c)
  /\x1B\[[?>]?[\d;]+c/g,
  // Bare DA tail (`1;2c`, `0;276;0c`, …) — the digit/semicolon prefix
  // that survives readline stripping the unprintable `\x1b[?`. Required
  // because the prefix-stripped tail no longer matches the patterns
  // above; without this the user sees the literal text at the prompt
  // even when terminalQueryResponses no longer answers the query.
  /(?:^|[^A-Za-z0-9])\d+(?:;\d+)+c(?![A-Za-z0-9])/g,
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

// Track DECTCEM (cursor visibility, `?25`) and alternate-screen
// (`?47` / `?1047` / `?1049`) state across written bytes. The desktop
// terminal is meant for shell interaction; a stuck `?25l` outside the
// alt-screen (e.g. an unmatched hide leaked by a spinner inside
// `erun open`, helm, kubectl, git over SSH, or the deploy pipeline)
// surfaces as a missing cursor at the bash prompt. The replay path
// uses scanCursorVisibility to detect that state and append a
// well-formed `?25h` so the user sees a cursor again; live TUIs are
// untouched because they sit inside the alternate screen.
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

export function filterTerminalDisplayData(
  sessions: TerminalSessionRegistry,
  sessionId: number,
  data: Uint8Array,
): TerminalWriteData | null {
  data = stripTerminalResponses(data);
  const debugMode = sessions.debugMode(sessionId);
  if (!debugMode) {
    return data;
  }
  if (debugMode === 'hidden') {
    const filter = sessions.debugFilter(sessionId);
    if (filter.released) {
      return data;
    }
    const text = new TextDecoder().decode(data);
    const output = filter.pending + text;
    const promptIndex = interactivePromptIndex(output);
    if (promptIndex === -1) {
      filter.pending = output.slice(-512);
      sessions.setDebugFilter(sessionId, filter);
      return null;
    }
    filter.released = true;
    filter.pending = '';
    sessions.setDebugFilter(sessionId, filter);
    return output.slice(promptIndex);
  }
  const filter = sessions.debugFilter(sessionId);
  if (filter.released) {
    return data;
  }

  const text = new TextDecoder().decode(data);
  const output = filter.pending + text;
  const titleIndex = output.indexOf('\x1B]0;');
  if (titleIndex === -1) {
    filter.pending = output.slice(-16);
    sessions.setDebugFilter(sessionId, filter);
    return null;
  }

  filter.released = true;
  filter.pending = '';
  sessions.setDebugFilter(sessionId, filter);
  return output.slice(titleIndex);
}
