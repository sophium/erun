import { FitAddon } from '@xterm/addon-fit';
import { Terminal, type IDisposable } from '@xterm/xterm';

import { idleApi } from './api/idleApi';
import { sessionApi } from './api/sessionApi';
import { boot, reloadStateAfterEnvironmentChange } from './bootThunks';
import { appendDebugOutput as appendDebugOutputThunk } from './debugThunks';
import { removeTab as removeTabThunk } from './tabsThunks';
import { selectEnvironmentExists, selectSelectedIsPendingFor } from './selectors';
import { setDoctorAll } from './slices/doctorSlice';
import { setIdleStatus } from './slices/idleSlice';
import { setReconnect, setSelectedDiffPath } from './slices/reviewSlice';
import { setSelected } from './slices/selectionSlice';
import { store } from './store';
import { thunkExtra } from './thunkExtra';
import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { ResizeSession, SendSessionInput } from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { fileToBase64, decodeBase64Bytes, isTerminalPasteTarget, pastedImageFiles } from './clipboard';
import { readError } from './errors';
import {
  hideTerminalMessage,
  showNotification,
  showTerminalFailure,
  showTerminalMessage,
} from './notificationThunks';
import { openSelection, selectTerminalTab as selectTerminalTabThunk } from './sessionThunks';
import { visibleDiffPath } from './reviewDiffNavigation';
import { registerTerminalQueryResponseHandlers } from './terminalQueryResponses';
import type {
  AppStatusPayload,
  EnvironmentInitializedPayload,
  MountElements,
  TerminalDataDisposable,
  TerminalExitSelections,
  TerminalWriteData,
} from './model';
import {
  MAX_DEBUG_HEIGHT,
  MAX_FILES_WIDTH,
  MIN_DEBUG_HEIGHT,
  MIN_FILES_WIDTH,
  MIN_REVIEW_WIDTH,
  computeMaxReviewWidth,
} from './state';

import { clamp } from './storage';
import {
  classifiedTerminalFailure,
  decodeDebugOutput,
  failedTerminalExitReason,
  statusForTerminalOutput,
  successfulTerminalExitReason,
  terminalExitHasTrackedSelection,
} from './terminalStatus';
import { failedTerminalOutput, filterTerminalDisplayData } from './terminalBuffers';
import { selectionKey } from './versionSuggestions';
import type {
  TerminalExitPayload,
  TerminalOutputPayload,
  UISelection,
} from '@/types';

const REVIEW_DIFF_REFRESH_INTERVAL_MS = 5000;

export class TerminalController {
  readonly sessions = new TerminalSessionRegistry();
  private terminal: Terminal | null = null;
  private fitAddon: FitAddon | null = null;
  private terminalRoot: HTMLDivElement | null = null;
  private _terminalPane: HTMLElement | null = null;
  private _reviewView: HTMLElement | null = null;
  private reviewMain: HTMLDivElement | null = null;
  private _diffList: HTMLDivElement | null = null;
  private resizeObserver: ResizeObserver | null = null;
  private resizeTimer = 0;
  private reviewScrollFrame = 0;
  private idleStatusTimer = 0;
  private reviewDiffRefreshTimer = 0;
  private idleStatusRequest = 0;
  private bootStarted = false;
  private terminalDataDisposable: TerminalDataDisposable | null = null;
  private terminalQueryResponseDisposables: IDisposable[] = [];
  private terminalOutputOff: (() => void) | null = null;
  private terminalExitOff: (() => void) | null = null;
  private appStatusOff: (() => void) | null = null;
  private reconnectLineOff: (() => void) | null = null;
  private environmentInitializedOff: (() => void) | null = null;
  private environmentInitFailedOff: (() => void) | null = null;
  private environmentsChangedOff: (() => void) | null = null;
  private pasteHandler: ((event: ClipboardEvent) => void) | null = null;

  constructor() {
    thunkExtra.controller = this;
  }

  // Public ref accessors used by layoutThunks/reviewThunks. These point to the
  // live DOM elements registered in mount(); their nullability matches the
  // pre-mount state.
  get terminalPane(): HTMLElement | null {
    return this._terminalPane;
  }

  get reviewView(): HTMLElement | null {
    return this._reviewView;
  }

  get diffList(): HTMLDivElement | null {
    return this._diffList;
  }

  terminalSize(): { cols: number; rows: number } {
    return { cols: this.terminal?.cols || 80, rows: this.terminal?.rows || 24 };
  }

  fitTerminal(): void {
    this.fitAddon?.fit();
  }

  mount(elements: MountElements): () => void {
    this.terminalRoot = elements.terminalRoot;
    this._terminalPane = elements.terminalPane;
    this._reviewView = elements.reviewView;
    this.reviewMain = elements.reviewMain;
    this._diffList = elements.diffList;
    this.applyLayoutVars();

    if (this.terminal) {
      this.queueTerminalResize();
      return () => {};
    }

    this.terminal = new Terminal({
      allowProposedApi: false,
      cursorBlink: true,
      fontFamily: 'ui-monospace, SFMono-Regular, SF Mono, Menlo, Monaco, Consolas, Liberation Mono, monospace',
      fontSize: 13,
      lineHeight: 1.18,
      theme: {
        background: 'oklch(0 0 0)',
      },
    });
    this.fitAddon = new FitAddon();
    this.terminal.loadAddon(this.fitAddon);
    this.terminal.open(elements.terminalRoot);
    this.fitAddon.fit();

    this.terminalQueryResponseDisposables = registerTerminalQueryResponseHandlers(
      this.terminal,
      (data) => SendSessionInput(store.getState().terminal.sessionId, data),
      (error) => store.dispatch(showTerminalMessage(readError(error))),
    );
    this.terminalDataDisposable = this.terminal.onData((data) => {
      SendSessionInput(store.getState().terminal.sessionId, data).catch((error: unknown) => {
        store.dispatch(showTerminalMessage(readError(error)));
      });
    });

    this.pasteHandler = (event: ClipboardEvent) => {
      void this.handleTerminalPaste(event).catch((error: unknown) => {
        store.dispatch(showTerminalMessage(readError(error)));
      });
    };
    elements.terminalRoot.addEventListener('paste', this.pasteHandler, true);

    this.resizeObserver = new ResizeObserver(() => {
      this.queueTerminalResize();
    });
    this.resizeObserver.observe(elements.terminalRoot);
    window.addEventListener('resize', this.queueTerminalResize);

    this.terminalOutputOff = EventsOn('terminal-output', (payload: TerminalOutputPayload) => {
      this.handleTerminalOutput(payload);
    });
    this.terminalExitOff = EventsOn('terminal-exit', (payload: TerminalExitPayload) => {
      void this.handleTerminalExit(payload);
    });
    this.appStatusOff = EventsOn('app-status', (payload: AppStatusPayload) => {
      this.handleAppStatus(payload);
    });
    this.reconnectLineOff = EventsOn('mcp-reconnect-line', (line: string) => {
      this.handleReconnectLine(line);
    });
    this.environmentInitializedOff = EventsOn('environment-initialized', (payload: EnvironmentInitializedPayload) => {
      void this.handleEnvironmentInitialized(payload);
    });
    this.environmentInitFailedOff = EventsOn('environment-init-failed', (payload: EnvironmentInitializedPayload) => {
      this.handleEnvironmentInitFailed(payload);
    });
    this.environmentsChangedOff = EventsOn('environments-changed', () => {
      void store.dispatch(reloadStateAfterEnvironmentChange());
    });

    if (!this.bootStarted) {
      this.bootStarted = true;
      void store.dispatch(boot());
    }
    this.scheduleIdleStatusPoll(0);

    return () => this.unmountTerminal();
  }

  private unmountTerminal(): void {
    window.removeEventListener('resize', this.queueTerminalResize);
    this.resizeObserver?.disconnect();
    this.terminalDataDisposable?.dispose();
    for (const disposable of this.terminalQueryResponseDisposables) {
      disposable.dispose();
    }
    this.terminalOutputOff?.();
    this.terminalExitOff?.();
    this.appStatusOff?.();
    this.reconnectLineOff?.();
    this.environmentInitializedOff?.();
    this.environmentInitFailedOff?.();
    this.environmentsChangedOff?.();
    window.clearTimeout(this.idleStatusTimer);
    this.stopReviewDiffRefresh();
    if (this.pasteHandler && this.terminalRoot) {
      this.terminalRoot.removeEventListener('paste', this.pasteHandler, true);
    }
    this.terminalOutputOff = null;
    this.terminalExitOff = null;
    this.appStatusOff = null;
    this.reconnectLineOff = null;
    this.environmentInitializedOff = null;
    this.environmentInitFailedOff = null;
    this.environmentsChangedOff = null;
    this.terminalQueryResponseDisposables = [];
    this.terminal?.dispose();
    this.terminal = null;
    this.fitAddon = null;
  }

  focusTerminalSoon(): void {
    window.setTimeout(() => {
      this.terminal?.focus();
      window.requestAnimationFrame(() => this.terminal?.focus());
      window.setTimeout(() => this.terminal?.focus(), 80);
    }, 0);
  }

  private scheduleIdleStatusPoll(delay = 1000): void {
    window.clearTimeout(this.idleStatusTimer);
    this.idleStatusTimer = window.setTimeout(() => {
      void this.refreshIdleStatus();
    }, delay);
  }

  async refreshIdleStatus(): Promise<void> {
    const selection = store.getState().selection.selected;
    const request = ++this.idleStatusRequest;
    if (!selection) {
      this.clearIdleStatus();
      this.scheduleIdleStatusPoll();
      return;
    }

    try {
      const status = await store
        .dispatch(idleApi.endpoints.getIdleStatus.initiate(selection, { forceRefetch: true }))
        .unwrap();
      if (this.isCurrentIdleStatusRequest(request, selection)) {
        store.dispatch(setIdleStatus(status));
      }
    } catch {
      this.clearCurrentIdleStatusRequest(request);
    } finally {
      if (request === this.idleStatusRequest) {
        this.scheduleIdleStatusPoll();
      }
    }
  }

  private clearIdleStatus(): void {
    if (!store.getState().idle.idleStatus) {
      return;
    }
    store.dispatch(setIdleStatus(null));
  }

  private clearCurrentIdleStatusRequest(request: number): void {
    if (request === this.idleStatusRequest) {
      this.clearIdleStatus();
    }
  }

  private isCurrentIdleStatusRequest(request: number, selection: UISelection): boolean {
    const selected = store.getState().selection.selected;
    return request === this.idleStatusRequest && selected?.tenant === selection.tenant && selected.environment === selection.environment;
  }

  resetTerminal(): void {
    this.terminal?.reset();
    this.terminal?.clear();
  }

  private handleAppStatus(payload: AppStatusPayload): void {
    const message = String(payload?.message || '').trim();
    if (!message) {
      return;
    }
    store.dispatch(appendDebugOutputThunk(`[status] ${message}\n`));
    store.dispatch(showTerminalMessage(message, payload.busy === true));
  }

  // Fires when the backend's PTY trace handler observes
  // `==> Initialized <tenant>/<env>` from a piped `erun init` command,
  // or when the config-file watcher detects a new env. Reload state so
  // the new env appears in the sidebar, surface a success toast
  // (Nielsen #1 visibility of system status), then open the selection
  // so the ERun and AI tabs spawn against the now-existing config.
  // See erun-ui/AGENTS.md § "Command Completion And State-Refresh
  // Wiring".
  private async handleEnvironmentInitialized(payload: EnvironmentInitializedPayload): Promise<void> {
    const tenant = String(payload?.tenant || '').trim();
    const environment = String(payload?.environment || '').trim();
    if (!tenant || !environment) {
      return;
    }
    await store.dispatch(reloadStateAfterEnvironmentChange());
    if (!selectEnvironmentExists(store.getState(), tenant, environment)) {
      return;
    }
    store.dispatch(showNotification('success', `Created ${tenant} / ${environment}.`));
    try {
      await store.dispatch(openSelection({ tenant, environment }));
    } catch (error) {
      store.dispatch(showTerminalMessage(readError(error)));
    }
  }

  // Fires when the backend's PTY trace handler observes
  // `==> Initialization failed <tenant>/<env>`. Surfaces an error toast
  // (Nielsen #1 + #9) and reverts the optimistic state.selected so the
  // sidebar's "creating ..." placeholder row disappears.
  private handleEnvironmentInitFailed(payload: EnvironmentInitializedPayload): void {
    const tenant = String(payload?.tenant || '').trim();
    const environment = String(payload?.environment || '').trim();
    if (!tenant || !environment) {
      return;
    }
    store.dispatch(showNotification('error', `Failed to create ${tenant} / ${environment}. See the Local tab and the activity drawer for details.`));
    if (selectSelectedIsPendingFor(store.getState(), tenant, environment)) {
      store.dispatch(setSelected(null));
    }
  }

  private handleTerminalOutput(payload: TerminalOutputPayload): void {
    if (!payload) {
      return;
    }
    const data = decodeBase64Bytes(payload.data);
    this.sessions.appendSessionBuffer(payload.sessionId, data);
    const debugOutput = decodeDebugOutput(data);
    store.dispatch(appendDebugOutputThunk(debugOutput, payload.sessionId));
    this.updateOpenStatusFromOutput(payload.sessionId, debugOutput);
    const displayData = filterTerminalDisplayData(this.sessions, payload.sessionId, data);
    if (displayData) {
      this.sessions.appendDisplayBuffer(payload.sessionId, displayData);
    }
    const state = store.getState();
    if (payload.sessionId !== state.terminal.sessionId) {
      return;
    }
    if (!displayData) {
      return;
    }
    if (state.terminalStatus.terminalMessage && !state.terminalStatus.terminalCopyOutput) {
      store.dispatch(hideTerminalMessage());
    }
    this.terminal?.write(displayData);
  }

  private async handleTerminalExit(payload: TerminalExitPayload): Promise<void> {
    if (!payload) {
      return;
    }
    const selections = this.takeTerminalExitSelections(payload.sessionId);
    const reason = this.terminalExitReason(payload, selections);
    const failedOutput = this.recordTerminalExit(payload, reason, selections);
    this.dropExitedSessionFromTabs(payload.sessionId, selections.openSelection);
    this.recordDoctorOutcome(payload, selections);

    if (selections.sshdInitSelection) {
      await store.dispatch(reloadStateAfterEnvironmentChange());
    }
    if (payload.sessionId !== store.getState().terminal.sessionId) {
      return;
    }
    if (await this.handleSuccessfulTerminalExit(payload, reason, selections)) {
      return;
    }
    if (payload.reason && terminalExitHasTrackedSelection(selections)) {
      const failure = classifiedTerminalFailure(payload.reason, reason, failedOutput, selections.openSelection);
      store.dispatch(showTerminalFailure(failure.message, failure.detail, failedOutput, failure.action, failure.retrySelection));
      return;
    }
    store.dispatch(showTerminalMessage(reason));
  }

  private recordDoctorOutcome(payload: TerminalExitPayload, selections: TerminalExitSelections): void {
    const selection = selections.doctorSelection;
    if (!selection) {
      return;
    }
    const key = selectionKey(selection);
    const reason = (payload.reason || '').trim();
    const lastDoctorBySelection = store.getState().doctor.lastDoctorBySelection;
    store.dispatch(setDoctorAll({
      lastDoctorBySelection: {
        ...lastDoctorBySelection,
        [key]: {
          ranAt: Date.now(),
          success: !reason,
          message: reason,
        },
      },
    }));
  }

  private takeTerminalExitSelections(sessionId: number): TerminalExitSelections {
    return this.sessions.takeExitSelections(sessionId);
  }

  private dropExitedSessionFromTabs(sessionId: number, openSelection: UISelection | undefined): void {
    if (!openSelection) {
      return;
    }
    const key = selectionKey(openSelection);
    const remaining = store.dispatch(removeTabThunk(key, sessionId));
    if (store.getState().terminal.sessionId !== sessionId) {
      return;
    }
    const next = remaining[remaining.length - 1];
    if (next) {
      store.dispatch(selectTerminalTabThunk(next.sessionId));
    }
  }

  private recordTerminalExit(payload: TerminalExitPayload, reason: string, selections: TerminalExitSelections): string {
    this.sessions.recordExitReason(payload.sessionId, reason);
    if (!payload.reason || !terminalExitHasTrackedSelection(selections)) {
      return '';
    }
    const failedOutput = failedTerminalOutput(this.sessions, payload.sessionId, reason);
    if (failedOutput) {
      this.sessions.recordExitOutput(payload.sessionId, failedOutput);
    }
    return failedOutput;
  }

  private async handleSuccessfulTerminalExit(payload: TerminalExitPayload, reason: string, selections: TerminalExitSelections): Promise<boolean> {
    if (payload.reason) {
      return false;
    }
    if (selections.sshdInitSelection) {
      store.dispatch(showTerminalMessage(reason));
      return true;
    }
    return false;
  }

  private terminalExitReason(payload: TerminalExitPayload, selections: TerminalExitSelections): string {
    if (payload.reason) {
      return failedTerminalExitReason(payload.reason, selections);
    }
    return successfulTerminalExitReason(selections);
  }

  private updateOpenStatusFromOutput(sessionId: number, output: string): void {
    if (!output || !this.sessions.isOpenSession(sessionId) || store.getState().terminalStatus.terminalCopyOutput) {
      return;
    }
    const status = statusForTerminalOutput(output);
    if (!status) {
      return;
    }
    store.dispatch(showTerminalMessage(status, true));
  }

  // handleReconnectLine appends a status line from the reconnect PTY into the
  // reconnect dialog while it is running. Kept on the controller because it
  // wires the EventsOn('mcp-reconnect-line') subscription.
  private handleReconnectLine(line: string): void {
    const trimmed = (line || '').trim();
    if (!trimmed) {
      return;
    }
    const reconnect = store.getState().review.reconnect;
    if (reconnect.status !== 'running') {
      return;
    }
    store.dispatch(setReconnect({ ...reconnect, lastLine: trimmed }));
  }

  layoutCallbacks(): {
    applyLayoutVars: () => void;
    focusTerminalSoon: () => void;
    queueTerminalResize: () => void;
  } {
    return {
      applyLayoutVars: () => this.applyLayoutVars(),
      focusTerminalSoon: () => this.focusTerminalSoon(),
      queueTerminalResize: this.queueTerminalResize,
    };
  }

  applyLayoutVars(): void {
    const root = document.documentElement;
    const layout = store.getState().layout;
    root.style.setProperty('--sidebar-width', `${layout.sidebarHidden ? 0 : layout.sidebarWidth}px`);
    root.style.setProperty('--review-width', `${this.clampedReviewWidth()}px`);
    root.style.setProperty('--files-width', `${this.clampedFilesWidth()}px`);
    root.style.setProperty('--debug-height', `${this.clampedDebugHeight()}px`);
  }

  private clampedReviewWidth(): number {
    const layout = store.getState().layout;
    const effectiveSidebar = layout.sidebarHidden ? 0 : layout.sidebarWidth;
    const maxWidth = computeMaxReviewWidth(window.innerWidth, effectiveSidebar);
    return clamp(layout.reviewWidth, MIN_REVIEW_WIDTH, maxWidth);
  }

  private clampedFilesWidth(): number {
    const layout = store.getState().layout;
    const reviewWidth = this._reviewView?.getBoundingClientRect().width || layout.reviewWidth;
    const maxFilesForReview = reviewWidth > 0 ? reviewWidth - 260 : MAX_FILES_WIDTH;
    return clamp(layout.filesWidth, MIN_FILES_WIDTH, Math.max(MIN_FILES_WIDTH, Math.min(MAX_FILES_WIDTH, maxFilesForReview)));
  }

  private clampedDebugHeight(): number {
    const paneHeight = this._terminalPane?.getBoundingClientRect().height || 0;
    const maxDebugForPane = paneHeight > 0 ? paneHeight - 120 : MAX_DEBUG_HEIGHT;
    return clamp(store.getState().layout.debugHeight, MIN_DEBUG_HEIGHT, Math.max(MIN_DEBUG_HEIGHT, Math.min(MAX_DEBUG_HEIGHT, maxDebugForPane)));
  }

  queueTerminalResize = (): void => {
    window.clearTimeout(this.resizeTimer);
    this.resizeTimer = window.setTimeout(() => {
      this.applyLayoutVars();
      this.fitAddon?.fit();
      const sessionId = store.getState().terminal.sessionId;
      if (sessionId > 0 && this.terminal) {
        ResizeSession(sessionId, this.terminal.cols, this.terminal.rows).catch(() => {
        });
      }
    }, 40);
  };

  queueVisibleDiffSelectionUpdate(): void {
    if (this.reviewScrollFrame > 0) {
      return;
    }
    this.reviewScrollFrame = window.requestAnimationFrame(() => {
      this.reviewScrollFrame = 0;
      this.updateSelectedDiffPathFromScroll();
    });
  }

  private updateSelectedDiffPathFromScroll(): void {
    const path = visibleDiffPath(this._diffList, this.reviewMain);
    if (!path || path === store.getState().review.selectedDiffPath) {
      return;
    }
    store.dispatch(setSelectedDiffPath(path));
  }

  // Review-diff refresh timer accessors. reviewThunks owns the polling logic
  // but the timer field stays here so unmountTerminal() can clear it.
  stopReviewDiffRefresh(): void {
    window.clearTimeout(this.reviewDiffRefreshTimer);
    this.reviewDiffRefreshTimer = 0;
  }

  cancelReviewDiffRefresh(): void {
    window.clearTimeout(this.reviewDiffRefreshTimer);
    this.reviewDiffRefreshTimer = 0;
  }

  scheduleReviewDiffRefreshTimer(callback: () => void, delay: number = REVIEW_DIFF_REFRESH_INTERVAL_MS): void {
    window.clearTimeout(this.reviewDiffRefreshTimer);
    this.reviewDiffRefreshTimer = window.setTimeout(callback, delay);
  }

  private async handleTerminalPaste(event: ClipboardEvent): Promise<void> {
    if (!this.terminalRoot || !isTerminalPasteTarget(this.terminalRoot, event.target)) {
      return;
    }

    const images = pastedImageFiles(event);
    if (images.length === 0) {
      return;
    }

    event.preventDefault();
    const sessionId = store.getState().terminal.sessionId;
    if (sessionId <= 0) {
      return;
    }
    const paths: string[] = [];
    for (const image of images) {
      const result = await store
        .dispatch(
          sessionApi.endpoints.savePastedImage.initiate({
            sessionId,
            payload: {
              data: await fileToBase64(image),
              mimeType: image.type,
              name: image.name,
            },
          }),
        )
        .unwrap();
      if (result.path) {
        paths.push(result.path);
      }
    }
    if (paths.length === 0) {
      return;
    }
    await SendSessionInput(sessionId, `${paths.join(' ')} `);
    this.focusTerminalSoon();
  }

  writeTerminalBuffer(chunks: TerminalWriteData[]): void {
    for (const chunk of chunks) {
      this.terminal?.write(chunk);
    }
  }

}
