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
  sessions.replaceDisplayBuffer(sessionId, displayBuffer);
}

// Strip xterm DSR / OSC query *responses* that leak into the displayed
// output. A tool inside the PTY sends a query (e.g. CPR `\x1b[6n`, or
// OSC 11 background-color query); xterm answers via onData; the answer
// lands in the PTY's stdin, the shell echoes it as if the user typed it,
// and the echo comes back through the output stream as visible gibberish
// like `^[[51;1R` or `^[]11;rgb:0000/0000/0000^[\`. Stripping the
// response patterns at the display boundary keeps the visible buffer
// clean without changing what the tools see.
const TERMINAL_RESPONSE_PATTERNS: RegExp[] = [
  // CSI Cursor Position Report response: ESC [ row ; col R
  /\x1B\[\d+;\d+R/g,
  // CSI Device Status Report response: ESC [ <n> n  (e.g. 0n)
  /\x1B\[\d+n/g,
  // OSC 10/11 (foreground / background color) responses terminated by
  // ST (ESC \) or BEL (0x07).
  /\x1B\](?:10|11);rgb:[0-9a-fA-F/]+(?:\x1B\\|\x07)/g,
  // Primary Device Attributes / Secondary Device Attributes responses
  // (CSI ? <params> c)
  /\x1B\[\?[\d;]+c/g,
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
