import type { DebugOpenFilter, DebugSessionMode, TerminalExitSelections, TerminalWriteData } from './model';
import { selectionKey } from './versionSuggestions';
import type { UISelection } from '@/types';

export class TerminalSessionRegistry {
  private readonly initSessionSelections = new Map<number, UISelection>();
  private readonly deploySessionSelections = new Map<number, UISelection>();
  private readonly sshdInitSessionSelections = new Map<number, UISelection>();
  private readonly doctorSessionSelections = new Map<number, UISelection>();
  private readonly openSessionSelections = new Map<number, UISelection>();
  private readonly cloudInitSessions = new Set<number>();
  private readonly selectionSessions = new Map<string, number>();
  private readonly sessionBuffers = new Map<number, Uint8Array[]>();
  private readonly sessionDisplayBuffers = new Map<number, TerminalWriteData[]>();
  private readonly sessionExitReasons = new Map<number, string>();
  private readonly sessionExitOutputs = new Map<number, string>();
  private readonly debugOpenFilters = new Map<number, DebugOpenFilter>();
  private readonly debugSessionModes = new Map<number, DebugSessionMode>();

  knownSelectionSession(key: string): number {
    return this.selectionSessions.get(key) || 0;
  }

  trackOpenSession(key: string, sessionId: number, selection: UISelection): void {
    this.selectionSessions.set(key, sessionId);
    this.openSessionSelections.set(sessionId, selection);
  }

  isOpenSession(sessionId: number): boolean {
    return this.openSessionSelections.has(sessionId);
  }

  trackSSHDInitSession(sessionId: number, selection: UISelection): void {
    this.sshdInitSessionSelections.set(sessionId, selection);
  }

  trackDoctorSession(sessionId: number, selection: UISelection): void {
    this.doctorSessionSelections.set(sessionId, selection);
  }

  trackCloudInitSession(sessionId: number): void {
    this.cloudInitSessions.add(sessionId);
  }

  trackInitSession(sessionId: number, selection: UISelection): void {
    this.initSessionSelections.set(sessionId, selection);
  }

  trackDeploySession(sessionId: number, selection: UISelection): void {
    this.deploySessionSelections.set(sessionId, selection);
  }

  appendSessionBuffer(sessionId: number, data: Uint8Array): void {
    const existing = this.sessionBuffers.get(sessionId) || [];
    existing.push(data);
    this.sessionBuffers.set(sessionId, existing);
  }

  sessionBuffer(sessionId: number): Uint8Array[] {
    return this.sessionBuffers.get(sessionId) || [];
  }

  appendDisplayBuffer(sessionId: number, data: TerminalWriteData): void {
    const displayBuffer = this.sessionDisplayBuffers.get(sessionId) || [];
    displayBuffer.push(data);
    this.sessionDisplayBuffers.set(sessionId, displayBuffer);
  }

  displayBuffer(sessionId: number): TerminalWriteData[] {
    return this.sessionDisplayBuffers.get(sessionId) || [];
  }

  replaceDisplayBuffer(sessionId: number, chunks: TerminalWriteData[]): void {
    if (chunks.length > 0) {
      this.sessionDisplayBuffers.set(sessionId, chunks);
      return;
    }
    this.sessionDisplayBuffers.delete(sessionId);
  }

  exitReason(sessionId: number): string {
    return this.sessionExitReasons.get(sessionId) || '';
  }

  exitOutput(sessionId: number): string {
    return this.sessionExitOutputs.get(sessionId) || '';
  }

  recordExitReason(sessionId: number, reason: string): void {
    this.sessionExitReasons.set(sessionId, reason);
  }

  recordExitOutput(sessionId: number, output: string): void {
    this.sessionExitOutputs.set(sessionId, output);
  }

  takeExitSelections(sessionId: number): TerminalExitSelections {
    const selections = {
      initSelection: this.initSessionSelections.get(sessionId),
      deploySelection: this.deploySessionSelections.get(sessionId),
      sshdInitSelection: this.sshdInitSessionSelections.get(sessionId),
      doctorSelection: this.doctorSessionSelections.get(sessionId),
      openSelection: this.openSessionSelections.get(sessionId),
      cloudInit: this.cloudInitSessions.has(sessionId),
    };
    this.initSessionSelections.delete(sessionId);
    this.deploySessionSelections.delete(sessionId);
    this.sshdInitSessionSelections.delete(sessionId);
    this.doctorSessionSelections.delete(sessionId);
    this.openSessionSelections.delete(sessionId);
    this.cloudInitSessions.delete(sessionId);
    this.debugSessionModes.delete(sessionId);
    if (selections.openSelection) {
      this.selectionSessions.delete(selectionKey(selections.openSelection));
    }
    return selections;
  }

  clearDebugFilter(sessionId: number): void {
    this.debugOpenFilters.delete(sessionId);
  }

  debugMode(sessionId: number): DebugSessionMode | undefined {
    return this.debugSessionModes.get(sessionId);
  }

  debugFilter(sessionId: number): DebugOpenFilter {
    return this.debugOpenFilters.get(sessionId) || { released: false, pending: '' };
  }

  setDebugFilter(sessionId: number, filter: DebugOpenFilter): void {
    this.debugOpenFilters.set(sessionId, filter);
  }

  registerDebugSession(sessionId: number, selection: UISelection, mode: DebugSessionMode): void {
    if (!selection.debug) {
      return;
    }
    this.debugSessionModes.set(sessionId, mode);
  }
}
