import type { UISelection } from '@/types';

import type { TerminalExitSelections, TerminalWriteData } from './model';
import {
  recordExitOutput as recordExitOutputAction,
  recordExitReason as recordExitReasonAction,
  takeExitSelections as takeExitSelectionsAction,
  trackDoctorSession as trackDoctorSessionAction,
  trackOpenSession as trackOpenSessionAction,
  trackSSHDInitSession as trackSSHDInitSessionAction,
} from './slices/sessionsSlice';
import { store } from './store';
import { selectionKey } from './versionSuggestions';

// The output buffers stay on this instance as Maps rather than in the Redux
// sessions slice on purpose: they are large and churn on every terminal
// write, so keeping them in the store would be a perf sink. All other
// per-session metadata lives in the slice.
export class TerminalSessionRegistry {
  private readonly sessionBuffers = new Map<number, Uint8Array[]>();
  private readonly sessionDisplayBuffers = new Map<number, TerminalWriteData[]>();

  knownSelectionSession(key: string): number {
    return store.getState().sessions.selectionToSessionId[key] ?? 0;
  }

  trackOpenSession(key: string, sessionId: number, selection: UISelection): void {
    store.dispatch(trackOpenSessionAction({ key, sessionId, selection }));
  }

  isOpenSession(sessionId: number): boolean {
    return store.getState().sessions.openSelections[sessionId] !== undefined;
  }

  trackSSHDInitSession(sessionId: number, selection: UISelection): void {
    store.dispatch(trackSSHDInitSessionAction({ sessionId, selection }));
  }

  trackDoctorSession(sessionId: number, selection: UISelection): void {
    store.dispatch(trackDoctorSessionAction({ sessionId, selection }));
  }

  appendSessionBuffer(sessionId: number, data: Uint8Array): void {
    const existing = this.sessionBuffers.get(sessionId) ?? [];
    existing.push(data);
    this.sessionBuffers.set(sessionId, existing);
  }

  sessionBuffer(sessionId: number): Uint8Array[] {
    return this.sessionBuffers.get(sessionId) ?? [];
  }

  appendDisplayBuffer(sessionId: number, data: TerminalWriteData): void {
    const displayBuffer = this.sessionDisplayBuffers.get(sessionId) ?? [];
    displayBuffer.push(data);
    this.sessionDisplayBuffers.set(sessionId, displayBuffer);
  }

  displayBuffer(sessionId: number): TerminalWriteData[] {
    return this.sessionDisplayBuffers.get(sessionId) ?? [];
  }

  replaceDisplayBuffer(sessionId: number, chunks: TerminalWriteData[]): void {
    if (chunks.length > 0) {
      this.sessionDisplayBuffers.set(sessionId, chunks);
      return;
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
      sshdInitSelection: state.sshdInitSelections[sessionId],
      doctorSelection: state.doctorSelections[sessionId],
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
