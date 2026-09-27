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
  // drops the display buffer entries that screen already carries, in the same
  // step: everything up to the capture is in `serialized`, so from here the
  // buffer holds only the delta a future switch-back needs to replay on top of
  // it.
  //
  // `keepFrom` is the index at which output that arrived after the switch was
  // dispatched begins. Those chunks were never written to the pane -- a session
  // the store no longer names buffers without writing -- so they are not in
  // `serialized` and are kept, not dropped with the captured prefix. The
  // default keeps nothing, which is what a caller with no such window wants.
  //
  // An empty capture is the one case where that premise does not hold, and it
  // is reachable whenever the session being switched away from has not painted
  // yet: xterm parses writes asynchronously, so a second switch dispatched
  // before the outgoing session's activation writes have flushed serializes a
  // terminal whose buffer those writes have not reached -- "" for a session
  // that was just switched to. Storing that would replace the session's
  // remembered screen with nothing and drop the display buffer that was the
  // only other copy of it, so every later switch back would render blank.
  // Refuse it: the session keeps whatever snapshot it already had, and the
  // buffer stays whole. That is what stops the blank -- it is not a promise
  // that the screen is replayed from the buffer, because the two activation
  // paths differ. A session with no snapshot falls back to activateSession's
  // cold path and replays the retained buffer, which is the screen the capture
  // could not reach. A session that already has a non-empty snapshot takes the
  // snapshot path instead -- [snapshot, ...delta] -- so it comes back on that
  // older screen plus whatever arrived since; output the reset or the refused
  // capture dropped stays dropped, and the pane is stale rather than blank.
  // That stale case is latent, not live: it needs the shared terminal reset
  // without a capture in between, and every reset caller runs either on
  // session 0 or on a session id just minted, which Go allocates
  // monotonically (a.nextSerial++) and never reuses. A future caller that
  // resets an active session or reuses an id would have to revisit this guard.
  captureSnapshot(
    sessionId: number,
    serialized: string,
    keepFrom = Number.POSITIVE_INFINITY,
  ): void {
    if (serialized === '') {
      return;
    }
    this.sessionSnapshots.set(sessionId, serialized);
    const buffered = this.sessionDisplayBuffers.get(sessionId);
    const chunks = buffered ? buffered.chunks.slice(keepFrom) : [];
    if (chunks.length === 0) {
      this.sessionDisplayBuffers.delete(sessionId);
      return;
    }
    this.sessionDisplayBuffers.set(sessionId, { chunks, ...totals(chunks) });
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
