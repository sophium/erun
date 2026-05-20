import { FitAddon } from '@xterm/addon-fit';
import { type IDisposable, Terminal } from '@xterm/xterm';

import { noop } from '@/lib/utils';
import type { TerminalExitPayload, TerminalOutputPayload } from '@/types';

import { ResizeSession, SendSessionInput } from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { sessionApi } from './api/sessionApi';
import { boot, reloadStateAfterEnvironmentChange } from './bootThunks';
import {
  decodeBase64Bytes,
  fileToBase64,
  isTerminalPasteTarget,
  pastedImageFiles,
} from './clipboard';
import { appendDebugOutput as appendDebugOutputThunk } from './debugThunks';
import { readError } from './errors';
import { refreshIdleStatus } from './idleThunks';
import type {
  AIActivityPayload,
  AppStatusPayload,
  EnvironmentInitializedPayload,
  MountElements,
  TerminalDataDisposable,
  TerminalWriteData,
} from './model';
import { showTerminalMessage } from './notificationThunks';
import { visibleDiffPath } from './reviewDiffNavigation';
import { setSelectedDiffPath } from './slices/reviewSlice';
import {
  computeMaxReviewWidth,
  MAX_DEBUG_HEIGHT,
  MAX_FILES_WIDTH,
  MIN_DEBUG_HEIGHT,
  MIN_FILES_WIDTH,
  MIN_REVIEW_WIDTH,
} from './state';
import { clamp } from './storage';
import { store } from './store';
import { filterTerminalDisplayData } from './terminalBuffers';
import { registerTerminalQueryResponseHandlers } from './terminalQueryResponses';
import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { decodeDebugOutput } from './terminalStatus';
import { thunkExtra } from './thunkExtra';
import {
  handleAIActivity,
  handleAppStatus,
  handleEnvironmentInitFailed,
  handleEnvironmentInitialized,
  handleReconnectLine,
  handleTerminalExit,
  hideTerminalMessageIfActive,
  updateOpenStatusFromOutput,
} from './wailsEventThunks';

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
  private aiActivityOff: (() => void) | null = null;
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
    return { cols: this.terminal?.cols ?? 80, rows: this.terminal?.rows ?? 24 };
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
      return noop;
    }

    this.terminal = new Terminal({
      allowProposedApi: false,
      cursorBlink: true,
      fontFamily:
        'ui-monospace, SFMono-Regular, SF Mono, Menlo, Monaco, Consolas, Liberation Mono, monospace',
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
      (error) => {
        store.dispatch(showTerminalMessage(readError(error)));
      },
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
      void store.dispatch(handleTerminalExit(payload));
    });
    this.appStatusOff = EventsOn('app-status', (payload: AppStatusPayload) => {
      store.dispatch(handleAppStatus(payload));
    });
    this.reconnectLineOff = EventsOn('mcp-reconnect-line', (line: string) => {
      store.dispatch(handleReconnectLine(line));
    });
    this.environmentInitializedOff = EventsOn(
      'environment-initialized',
      (payload: EnvironmentInitializedPayload) => {
        void store.dispatch(handleEnvironmentInitialized(payload));
      },
    );
    this.environmentInitFailedOff = EventsOn(
      'environment-init-failed',
      (payload: EnvironmentInitializedPayload) => {
        store.dispatch(handleEnvironmentInitFailed(payload));
      },
    );
    this.environmentsChangedOff = EventsOn('environments-changed', () => {
      void store.dispatch(reloadStateAfterEnvironmentChange());
    });
    this.aiActivityOff = EventsOn('ai-activity', (payload: AIActivityPayload) => {
      store.dispatch(handleAIActivity(payload));
    });

    if (!this.bootStarted) {
      this.bootStarted = true;
      void store.dispatch(boot());
    }
    this.scheduleIdleStatusPoll(0);

    return () => {
      this.unmountTerminal();
    };
  }

  private unmountTerminal(): void {
    window.removeEventListener('resize', this.queueTerminalResize);
    this.resizeObserver?.disconnect();
    this.terminalDataDisposable?.dispose();
    for (const disposable of this.terminalQueryResponseDisposables) {
      disposable.dispose();
    }
    this.detachWailsEventListeners();
    window.clearTimeout(this.idleStatusTimer);
    this.stopReviewDiffRefresh();
    if (this.pasteHandler && this.terminalRoot) {
      this.terminalRoot.removeEventListener('paste', this.pasteHandler, true);
    }
    this.terminalQueryResponseDisposables = [];
    this.terminal?.dispose();
    this.terminal = null;
    this.fitAddon = null;
  }

  private detachWailsEventListeners(): void {
    this.terminalOutputOff?.();
    this.terminalExitOff?.();
    this.appStatusOff?.();
    this.reconnectLineOff?.();
    this.environmentInitializedOff?.();
    this.environmentInitFailedOff?.();
    this.environmentsChangedOff?.();
    this.aiActivityOff?.();
    this.terminalOutputOff = null;
    this.terminalExitOff = null;
    this.appStatusOff = null;
    this.reconnectLineOff = null;
    this.environmentInitializedOff = null;
    this.environmentInitFailedOff = null;
    this.environmentsChangedOff = null;
    this.aiActivityOff = null;
  }

  focusTerminalSoon(): void {
    window.setTimeout(() => {
      this.terminal?.focus();
      window.requestAnimationFrame(() => this.terminal?.focus());
      window.setTimeout(() => this.terminal?.focus(), 80);
    }, 0);
  }

  // scheduleIdleStatusPoll holds the setTimeout cancellation handle for
  // the recursive idle-status poll. The state-touching part lives in the
  // refreshIdleStatus thunk; this method just arms the timer.
  scheduleIdleStatusPoll(delay = 1000): void {
    window.clearTimeout(this.idleStatusTimer);
    this.idleStatusTimer = window.setTimeout(() => {
      void store.dispatch(refreshIdleStatus());
    }, delay);
  }

  resetTerminal(): void {
    this.terminal?.reset();
    this.terminal?.clear();
  }

  // handleTerminalOutput stays on the controller because it does the
  // imperative xterm write and appends to the registry's per-session
  // buffer Maps (which are the perf-carveout that justifies the registry
  // existing at all). State-side effects are dispatched as thunks.
  private handleTerminalOutput(payload: TerminalOutputPayload): void {
    const data = decodeBase64Bytes(payload.data);
    this.sessions.appendSessionBuffer(payload.sessionId, data);
    const debugOutput = decodeDebugOutput(data);
    store.dispatch(appendDebugOutputThunk(debugOutput, payload.sessionId));
    store.dispatch(updateOpenStatusFromOutput(payload.sessionId, debugOutput));
    const displayData = filterTerminalDisplayData(this.sessions, payload.sessionId, data);
    if (displayData) {
      this.sessions.appendDisplayBuffer(payload.sessionId, displayData);
    }
    if (payload.sessionId !== store.getState().terminal.sessionId || !displayData) {
      return;
    }
    store.dispatch(hideTerminalMessageIfActive(payload.sessionId));
    this.terminal?.write(displayData);
  }

  layoutCallbacks(): {
    applyLayoutVars: () => void;
    focusTerminalSoon: () => void;
    queueTerminalResize: () => void;
  } {
    return {
      applyLayoutVars: () => {
        this.applyLayoutVars();
      },
      focusTerminalSoon: () => {
        this.focusTerminalSoon();
      },
      queueTerminalResize: this.queueTerminalResize,
    };
  }

  applyLayoutVars(): void {
    const root = document.documentElement;
    const layout = store.getState().layout;
    const sidebarPx = layout.sidebarHidden ? 0 : layout.sidebarWidth;
    root.style.setProperty('--sidebar-width', `${String(sidebarPx)}px`);
    root.style.setProperty('--review-width', `${String(this.clampedReviewWidth())}px`);
    root.style.setProperty('--files-width', `${String(this.clampedFilesWidth())}px`);
    root.style.setProperty('--debug-height', `${String(this.clampedDebugHeight())}px`);
  }

  private clampedReviewWidth(): number {
    const layout = store.getState().layout;
    const effectiveSidebar = layout.sidebarHidden ? 0 : layout.sidebarWidth;
    const maxWidth = computeMaxReviewWidth(window.innerWidth, effectiveSidebar);
    return clamp(layout.reviewWidth, MIN_REVIEW_WIDTH, maxWidth);
  }

  private clampedFilesWidth(): number {
    const layout = store.getState().layout;
    const reviewWidth = this._reviewView?.getBoundingClientRect().width ?? layout.reviewWidth;
    const maxFilesForReview = reviewWidth > 0 ? reviewWidth - 260 : MAX_FILES_WIDTH;
    return clamp(
      layout.filesWidth,
      MIN_FILES_WIDTH,
      Math.max(MIN_FILES_WIDTH, Math.min(MAX_FILES_WIDTH, maxFilesForReview)),
    );
  }

  private clampedDebugHeight(): number {
    const paneHeight = this._terminalPane?.getBoundingClientRect().height ?? 0;
    const maxDebugForPane = paneHeight > 0 ? paneHeight - 120 : MAX_DEBUG_HEIGHT;
    return clamp(
      store.getState().layout.debugHeight,
      MIN_DEBUG_HEIGHT,
      Math.max(MIN_DEBUG_HEIGHT, Math.min(MAX_DEBUG_HEIGHT, maxDebugForPane)),
    );
  }

  queueTerminalResize = (): void => {
    window.clearTimeout(this.resizeTimer);
    this.resizeTimer = window.setTimeout(() => {
      this.applyLayoutVars();
      this.fitAddon?.fit();
      const sessionId = store.getState().terminal.sessionId;
      if (sessionId > 0 && this.terminal) {
        ResizeSession(sessionId, this.terminal.cols, this.terminal.rows).catch(noop);
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

  scheduleReviewDiffRefreshTimer(
    callback: () => void,
    delay: number = REVIEW_DIFF_REFRESH_INTERVAL_MS,
  ): void {
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
