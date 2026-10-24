import type { UISelection } from '@/types';

import type { TerminalExitSelections, TerminalWriteData } from './model';
import {
  recordExitOutput as recordExitOutputAction,
  recordExitReason as recordExitReasonAction,
  takeExitSelections as takeExitSelectionsAction,
  trackOpenSession as trackOpenSessionAction,
} from './slices/sessionsSlice';
import { store } from './store';
import {
  countNewlines,
  MAX_RETAINED_BYTES,
  MAX_RETAINED_LINES,
  RETAINED_TRIM_TARGET,
  trimChunksWithinBudget,
} from './terminalBuffers';
import { selectionKey } from './versionSuggestions';

// Raw session output, kept only so failedTerminalOutput can report its tail.
interface RetainedRawBuffer {
  chunks: Uint8Array[];
  bytes: number;
}

interface RetainedDisplayBuffer {
  chunks: TerminalWriteData[];
  // Running totals kept in lockstep with `chunks` for an O(1) budget check.
  lines: number;
  bytes: number;
  // Monotonic chunk count; unlike an index it stays meaningful across head trims.
  appended: number;
}

function totals(chunks: TerminalWriteData[]): { lines: number; bytes: number } {
  let lines = 0;
  let bytes = 0;
  for (const chunk of chunks) {
    lines += countNewlines(chunk);
    bytes += chunk.length;
  }
  return { lines, bytes };
}

// Output buffers stay out of Redux: they are large and churn on every write.
export class TerminalSessionRegistry {
  private readonly sessionBuffers = new Map<number, RetainedRawBuffer>();
  private readonly sessionDisplayBuffers = new Map<number, RetainedDisplayBuffer>();
  // Last serialized screen captured on switch-away, so a switch back replays
  // only the delta since.
  private readonly sessionSnapshots = new Map<number, string>();
  // A queued switch-away can still capture a forgotten session; this refuses it.
  private readonly forgottenSessions = new Set<number>();

  knownSelectionSession(key: string): number {
    return store.getState().sessions.selectionToSessionId[key] ?? 0;
  }

  trackOpenSession(key: string, sessionId: number, selection: UISelection): void {
    store.dispatch(trackOpenSessionAction({ key, sessionId, selection }));
  }

  isOpenSession(sessionId: number): boolean {
    return store.getState().sessions.openSelections[sessionId] !== undefined;
  }

  // Bounded so a long-running session keeps only the tail an exit report needs.
  appendSessionBuffer(sessionId: number, data: Uint8Array): void {
    if (this.forgottenSessions.has(sessionId)) {
      return;
    }
    const existing = this.sessionBuffers.get(sessionId) ?? { chunks: [], bytes: 0 };
    existing.chunks.push(data);
    existing.bytes += data.length;
    if (existing.bytes > MAX_RETAINED_BYTES) {
      const trimmed = trimChunksWithinBudget(
        existing.chunks,
        Number.POSITIVE_INFINITY,
        MAX_RETAINED_BYTES * RETAINED_TRIM_TARGET,
      );
      existing.chunks = trimmed.chunks;
      existing.bytes = trimmed.bytes;
    }
    this.sessionBuffers.set(sessionId, existing);
  }

  sessionBuffer(sessionId: number): Uint8Array[] {
    return this.sessionBuffers.get(sessionId)?.chunks ?? [];
  }

  forgetSessionBuffer(sessionId: number): void {
    this.sessionBuffers.delete(sessionId);
  }

  // forgetSession drops a session's state for good; Go never reuses session ids.
  forgetSession(sessionId: number): void {
    this.forgottenSessions.add(sessionId);
    this.sessionBuffers.delete(sessionId);
    this.sessionDisplayBuffers.delete(sessionId);
    this.sessionSnapshots.delete(sessionId);
  }

  // Bounded at append time to TERMINAL_SCROLLBACK so idle sessions cannot grow
  // without limit; crossing the budget trims to RETAINED_TRIM_TARGET so the
  // rescan is amortized.
  appendDisplayBuffer(sessionId: number, data: TerminalWriteData): void {
    if (this.forgottenSessions.has(sessionId)) {
      return;
    }
    const existing = this.sessionDisplayBuffers.get(sessionId) ?? {
      chunks: [],
      lines: 0,
      bytes: 0,
      appended: 0,
    };
    existing.chunks.push(data);
    existing.lines += countNewlines(data);
    existing.bytes += data.length;
    // Counted before the trim and never decremented by it.
    existing.appended += 1;
    if (existing.lines > MAX_RETAINED_LINES || existing.bytes > MAX_RETAINED_BYTES) {
      ({
        chunks: existing.chunks,
        lines: existing.lines,
        bytes: existing.bytes,
      } = trimChunksWithinBudget(
        existing.chunks,
        MAX_RETAINED_LINES * RETAINED_TRIM_TARGET,
        MAX_RETAINED_BYTES * RETAINED_TRIM_TARGET,
      ));
    }
    this.sessionDisplayBuffers.set(sessionId, existing);
  }

  displayBuffer(sessionId: number): TerminalWriteData[] {
    return this.sessionDisplayBuffers.get(sessionId)?.chunks ?? [];
  }

  displayAppendedCount(sessionId: number): number {
    return this.sessionDisplayBuffers.get(sessionId)?.appended ?? 0;
  }

  snapshot(sessionId: number): string | undefined {
    return this.sessionSnapshots.get(sessionId);
  }

  // captureSnapshot stores the switch-away screen and drops the buffered chunks
  // it already contains. Chunks after `appendedAtDispatch` were never written to
  // the pane, so they are kept; counting from the tail survives head trims.
  // An empty capture (the session had not painted yet) is refused, keeping the
  // old snapshot and the whole buffer so the pane never comes back blank. This
  // assumes resets only hit session 0 or fresh ids, which Go never reuses.
  captureSnapshot(
    sessionId: number,
    serialized: string,
    appendedAtDispatch = Number.POSITIVE_INFINITY,
  ): void {
    if (serialized === '' || this.forgottenSessions.has(sessionId)) {
      return;
    }
    this.sessionSnapshots.set(sessionId, serialized);
    const buffered = this.sessionDisplayBuffers.get(sessionId);
    if (buffered) {
      // Fewer buffered than arrived means a trim reached into the window.
      const arrived = buffered.appended - appendedAtDispatch;
      const keep = Number.isFinite(arrived)
        ? buffered.chunks.slice(Math.max(0, buffered.chunks.length - arrived))
        : [];
      if (keep.length > 0) {
        this.sessionDisplayBuffers.set(sessionId, {
          chunks: keep,
          appended: buffered.appended,
          ...totals(keep),
        });
        return;
      }
    }
    this.sessionDisplayBuffers.delete(sessionId);
  }

  exitReason(sessionId: number): string {
    return store.getState().sessions.exitReasons[sessionId] ?? '';
  }

  exitOutput(sessionId: number): string {
    return store.getState().sessions.exitOutputs[sessionId] ?? '';
  }

  recordExitReason(sessionId: number, reason: string): void {
    store.dispatch(recordExitReasonAction({ sessionId, reason }));
  }

  recordExitOutput(sessionId: number, output: string): void {
    store.dispatch(recordExitOutputAction({ sessionId, output }));
  }

  takeExitSelections(sessionId: number): TerminalExitSelections {
    const state = store.getState().sessions;
    const openSelection = state.openSelections[sessionId];
    const selections: TerminalExitSelections = {
      openSelection,
      cloudInit: state.cloudInitSessions[sessionId] ?? null,
    };
    store.dispatch(
      takeExitSelectionsAction({
        sessionId,
        selectionKey: openSelection ? selectionKey(openSelection) : null,
      }),
    );
    return selections;
  }
}
