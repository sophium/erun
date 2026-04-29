import type { TerminalSessionRegistry } from './TerminalSessionRegistry';
import type { TerminalWriteData } from './model';
import { cleanTerminalOutput, interactivePromptIndex } from './terminalStatus';

export function failedTerminalOutput(sessions: TerminalSessionRegistry, sessionId: number, fallback: string): string {
  const chunks = sessions.sessionBuffer(sessionId);
  const decoder = new TextDecoder();
  const output = chunks.map((chunk) => decoder.decode(chunk, { stream: true })).join('') + decoder.decode();
  return cleanTerminalOutput(output) || fallback;
}

export function rebuildTerminalDisplayBuffer(sessions: TerminalSessionRegistry, sessionId: number): void {
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

export function filterTerminalDisplayData(
  sessions: TerminalSessionRegistry,
  sessionId: number,
  data: Uint8Array,
): TerminalWriteData | null {
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
