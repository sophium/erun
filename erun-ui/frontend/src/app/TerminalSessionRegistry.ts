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
  trimChunksToBudget,
} from './terminalBuffers';
import { selectionKey } from './versionSuggestions';

interface RetainedDisplayBuffer {
  chunks: TerminalWriteData[];
  // Running totals kept in lockstep with `chunks` so a budget check is O(1)
  // on the common (under-budget) path -- see appendDisplayBuffer.
  lines: number;
  bytes: number;
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

// The output buffers stay on this instance as Maps rather than in the Redux
// sessions slice on purpose: they are large and churn on every terminal
// write, so keeping them in the store would be a perf sink. All other
// per-session metadata lives in the slice.
export class TerminalSessionRegistry {
  private readonly sessionBuffers = new Map<number, Uint8Array[]>();
  private readonly sessionDisplayBuffers = new Map<number, RetainedDisplayBuffer>();
  // The last serialized screen+scrollback xterm captured for a session when
  // it was switched away from (see TerminalController.snapshotSession). A
  // switch back writes this once, then only the (already-bounded) display
  // buffer accumulated since -- O(delta since last visit), not O(session
  // history) (#1322). Absent for a session that has never been switched away
  // from yet; that session still replays its buffer from scratch once.
  private readonly sessionSnapshots = new Map<number, string>();

  knownSelectionSession(key: string): number {
    return store.getState().sessions.selectionToSessionId[key] ?? 0;
  }

  trackOpenSession(key: string, sessionId: number, selection: UISelection): void {
    store.dispatch(trackOpenSessionAction({ key, sessionId, selection }));
  }

  isOpenSession(sessionId: number): boolean {
    return store.getState().sessions.openSelections[sessionId] !== undefined;
  }

  appendSessionBuffer(sessionId: number, data: Uint8Array): void {
    const existing = this.sessionBuffers.get(sessionId) ?? [];
    existing.push(data);
    this.sessionBuffers.set(sessionId, existing);
  }

  sessionBuffer(sessionId: number): Uint8Array[] {
    return this.sessionBuffers.get(sessionId) ?? [];
  }

  // Bounded at append time, not only at replay time: an inactive session (an
  // orchestrator idling in the background, a build a nobody is watching) must
  // not grow this array without limit just because nothing has read it since.
  // The same budget xterm's own scrollback uses (TERMINAL_SCROLLBACK) --
  // retaining more than that is memory nothing will ever render.
  //
  // The budget check itself is O(1) on the common under-budget path (a
  // running total, not a re-scan of the whole array on every single output
  // chunk); trimChunksToBudget's O(retained size) rescan only runs on the
  // rarer occasions the running total actually crosses the budget.
  appendDisplayBuffer(sessionId: number, data: TerminalWriteData): void {
    const existing = this.sessionDisplayBuffers.get(sessionId) ?? {
      chunks: [],
      lines: 0,
      bytes: 0,
    };
    existing.chunks.push(data);
    existing.lines += countNewlines(data);
    existing.bytes += data.length;
    if (existing.lines > MAX_RETAINED_LINES || existing.bytes > MAX_RETAINED_BYTES) {
      const trimmed = trimChunksToBudget(existing.chunks, MAX_RETAINED_LINES, MAX_RETAINED_BYTES);
      if (trimmed !== existing.chunks) {
        existing.chunks = trimmed;
        ({ lines: existing.lines, bytes: existing.bytes } = totals(trimmed));
      }
    }
    this.sessionDisplayBuffers.set(sessionId, existing);
  }

  displayBuffer(sessionId: number): TerminalWriteData[] {
    return this.sessionDisplayBuffers.get(sessionId)?.chunks ?? [];
  }

  snapshot(sessionId: number): string | undefined {
    return this.sessionSnapshots.get(sessionId);
  }

  // captureSnapshot records the serialized screen a switch-away captured and
  // clears the display buffer in the same step: everything up to now is
  // captured in `serialized`, so from here the buffer holds only the delta a
  // future switch-back needs to replay on top of it.
  captureSnapshot(sessionId: number, serialized: string): void {
    this.sessionSnapshots.set(sessionId, serialized);
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
