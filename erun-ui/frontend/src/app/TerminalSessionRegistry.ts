import type { UISelection } from '@/types';

import type {
  DebugOpenFilter,
  DebugSessionMode,
  TerminalExitSelections,
  TerminalWriteData,
} from './model';
import {
  clearDebugFilter as clearDebugFilterAction,
  clearSessionDebug as clearSessionDebugAction,
  recordExitOutput as recordExitOutputAction,
  recordExitReason as recordExitReasonAction,
  registerDebugSession as registerDebugSessionAction,
  setDebugFilter as setDebugFilterAction,
  setSessionDebug as setSessionDebugAction,
  takeExitSelections as takeExitSelectionsAction,
  trackCloudInitSession as trackCloudInitSessionAction,
  trackDoctorSession as trackDoctorSessionAction,
  trackOpenSession as trackOpenSessionAction,
  trackSSHDInitSession as trackSSHDInitSessionAction,
} from './slices/sessionsSlice';
import { store } from './store';
import { selectionKey } from './versionSuggestions';

// TerminalSessionRegistry is a thin facade. Per-session *metadata* (which
// sessions are open/sshd/doctor/cloud-init, exit reasons/outputs, debug
// modes/filters, debug-header buffer) lives in the sessions slice — the
// methods below read/write through the store. Only the raw output buffers
// (Uint8Array[]) and the display-filtered TerminalWriteData[] arrays still
// live on this instance, as Maps: they are large, churn frequently, and
// were excluded from Redux deliberately for perf.
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

  trackCloudInitSession(sessionId: number): void {
    store.dispatch(trackCloudInitSessionAction(sessionId));
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
      cloudInit: !!state.cloudInitSessions[sessionId],
    };
    store.dispatch(
      takeExitSelectionsAction({
        sessionId,
        selectionKey: openSelection ? selectionKey(openSelection) : null,
      }),
    );
    return selections;
  }

  clearDebugFilter(sessionId: number): void {
    store.dispatch(clearDebugFilterAction(sessionId));
  }

  debugMode(sessionId: number): DebugSessionMode | undefined {
    return store.getState().sessions.debugModes[sessionId];
  }

  debugFilter(sessionId: number): DebugOpenFilter {
    return store.getState().sessions.debugFilters[sessionId] ?? { released: false, pending: '' };
  }

  setDebugFilter(sessionId: number, filter: DebugOpenFilter): void {
    store.dispatch(setDebugFilterAction({ sessionId, filter }));
  }

  registerDebugSession(sessionId: number, selection: UISelection, mode: DebugSessionMode): void {
    store.dispatch(registerDebugSessionAction({ sessionId, selection, mode }));
  }

  sessionDebug(sessionId: number): string {
    return store.getState().sessions.debugBuffers[sessionId] ?? '';
  }

  setSessionDebug(sessionId: number, value: string): void {
    store.dispatch(setSessionDebugAction({ sessionId, value }));
  }

  clearSessionDebug(sessionId: number): void {
    store.dispatch(clearSessionDebugAction(sessionId));
  }
}
