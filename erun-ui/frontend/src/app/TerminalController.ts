import { FitAddon } from '@xterm/addon-fit';
import { type IDisposable, Terminal } from '@xterm/xterm';

import { noop } from '@/lib/utils';
import type { TerminalExitPayload, TerminalOutputPayload } from '@/types';

import { ResizeSession, SendSessionInput } from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { sessionApi } from './api/sessionApi';
import { boot, reloadStateAfterEnvironmentChange } from './bootThunks';
import { decodeBase64Bytes, fileToBase64, isTerminalPasteTarget, pastedFiles } from './clipboard';
import { readError } from './errors';
import { refreshIdleStatus } from './idleThunks';
import type {
  AIActivityPayload,
  AppNotificationPayload,
  AppStatusPayload,
  EnvironmentInitializedPayload,
  EnvStatusPayload,
  MountElements,
  TerminalDataDisposable,
  TerminalWriteData,
} from './model';
import { showTerminalMessage } from './notificationThunks';
import { scrollSelectedTreeNodeIntoView, visibleDiffPath } from './reviewDiffNavigation';
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
import {
  bufferCursorVisibility,
  type CursorVisibilityState,
  filterTerminalDisplayData,
  scanCursorVisibility,
  SHOW_CURSOR_SEQUENCE,
} from './terminalBuffers';
import { registerTerminalQueryResponseHandlers } from './terminalQueryResponses';
import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { decodeTerminalOutput } from './terminalStatus';
import { TerminalWriteSourceQueue } from './TerminalWriteSourceQueue';
import { thunkExtra } from './thunkExtra';
import {
  handleAIActivity,
  handleAppNotification,
  handleAppStatus,
  handleEnvironmentInitFailed,
  handleEnvironmentInitialized,
  handleEnvStatus,
  handleReconnectLine,
  handleTerminalExit,
  hideTerminalMessageIfActive,
  updateOpenStatusFromOutput,
} from './wailsEventThunks';

const REVIEW_DIFF_REFRESH_INTERVAL_MS = 5000;

export class TerminalController {
  readonly sessions = new TerminalSessionRegistry();
  // Tracks the source session of each in-flight xterm write so terminal query
  // replies route back to the asking session, not the currently-selected one
  // (issue #347). See TerminalWriteSourceQueue and writeToTerminal().
  private readonly writeSources = new TerminalWriteSourceQueue();
  private terminal: Terminal | null = null;
  private fitAddon: FitAddon | null = null;
  private terminalRoot: HTMLDivElement | null = null;
  private _terminalPane: HTMLElement | null = null;
  private _reviewView: HTMLElement | null = null;
  private reviewMain: HTMLDivElement | null = null;
  private _diffList: HTMLDivElement | null = null;
  private treeContainer: HTMLDivElement | null = null;
  private resizeObserver: ResizeObserver | null = null;
  private resizeTimer = 0;
  private resizeFrame = 0;
  private reviewScrollFrame = 0;
  private idleStatusTimer = 0;
  private reviewDiffRefreshTimer = 0;
  private bootStarted = false;
  private terminalDataDisposable: TerminalDataDisposable | null = null;
  private terminalQueryResponseDisposables: IDisposable[] = [];
  private terminalOutputOff: (() => void) | null = null;
  private terminalExitOff: (() => void) | null = null;
  private appStatusOff: (() => void) | null = null;
  private appNotificationOff: (() => void) | null = null;
  private reconnectLineOff: (() => void) | null = null;
  private environmentInitializedOff: (() => void) | null = null;
  private environmentInitFailedOff: (() => void) | null = null;
  private environmentsChangedOff: (() => void) | null = null;
  private aiActivityOff: (() => void) | null = null;
  private envStatusOff: (() => void) | null = null;
  private pasteHandler: ((event: ClipboardEvent) => void) | null = null;
  // Track DECTCEM (`?25`) and alt-screen state across the bytes
  // written to xterm for the active session. When the active session
  // ends in "main screen + cursor hidden" with no further output, we
  // restore `?25h` so an unmatched hide leaked by `erun open`, helm,
  // kubectl, or a remote-side spinner doesn't strand the prompt with
  // no visible cursor. Alt-screen TUIs are exempt by design.
  private liveCursorState: CursorVisibilityState = { altScreen: false, cursorHidden: false };
  private cursorRestoreTimer = 0;
  private static readonly CURSOR_RESTORE_DELAY_MS = 250;

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

  // setTreeContainer registers the changed-files tree's scroll container so the
  // diff→tree scrollspy can keep the active node visible (#547). It is a
  // callback ref, not part of mount(): the tree container is conditionally
  // rendered (only while the Changed files section is open), so it mounts and
  // unmounts independently of the one-time controller mount — passing null on
  // unmount keeps the reference from going stale.
  setTreeContainer(element: HTMLDivElement | null): void {
    this.treeContainer = element;
  }

  terminalSize(): { cols: number; rows: number } {
    return { cols: this.terminal?.cols ?? 80, rows: this.terminal?.rows ?? 24 };
  }

  fitTerminal(): void {
    this.fitAddon?.fit();
    this.publishTerminalDims();
  }

  private publishTerminalDims(): void {
    if (!this.terminalRoot || !this.terminal) {
      return;
    }
    this.terminalRoot.dataset.terminalCols = String(this.terminal.cols);
    this.terminalRoot.dataset.terminalRows = String(this.terminal.rows);
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
    this.publishTerminalDims();

    this.terminalQueryResponseDisposables = registerTerminalQueryResponseHandlers(
      this.terminal,
      // Address the reply to the session whose output xterm is parsing right
      // now (writeSources head), falling back to the current selection when no
      // write is in flight. Reading the live selection here would misroute the
      // reply if the user switched sessions during a deferred parse (#347).
      (data) =>
        SendSessionInput(this.writeSources.current(store.getState().terminal.sessionId), data),
      (error) => {
        store.dispatch(showTerminalMessage(readError(error)));
      },
      // Suppress replies to queries re-parsed from a replayed display buffer:
      // the asking tool consumed the live reply long ago, so a second reply
      // would land on the session's shell as typed input (#484).
      () => this.writeSources.currentIsReplay(),
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
    this.appNotificationOff = EventsOn('app-notification', (payload: AppNotificationPayload) => {
      store.dispatch(handleAppNotification(payload));
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
    this.envStatusOff = EventsOn('env-status', (payload: EnvStatusPayload) => {
      store.dispatch(handleEnvStatus(payload));
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
    window.clearTimeout(this.resizeTimer);
    this.resizeTimer = 0;
    if (this.resizeFrame !== 0) {
      window.cancelAnimationFrame(this.resizeFrame);
      this.resizeFrame = 0;
    }
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
    // xterm drops pending write-completion callbacks on dispose, so reset the
    // source queue to avoid carrying a stale head into the next mount.
    this.writeSources.clear();
  }

  private detachWailsEventListeners(): void {
    const fields = [
      'terminalOutputOff',
      'terminalExitOff',
      'appStatusOff',
      'appNotificationOff',
      'reconnectLineOff',
      'environmentInitializedOff',
      'environmentInitFailedOff',
      'environmentsChangedOff',
      'aiActivityOff',
      'envStatusOff',
    ] as const;
    for (const field of fields) {
      this[field]?.();
      this[field] = null;
    }
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
    this.cancelCursorRestoreTimer();
    this.liveCursorState = { altScreen: false, cursorHidden: false };
  }

  private cancelCursorRestoreTimer(): void {
    if (this.cursorRestoreTimer !== 0) {
      window.clearTimeout(this.cursorRestoreTimer);
      this.cursorRestoreTimer = 0;
    }
  }

  private scheduleCursorRestoreIfStuck(): void {
    this.cancelCursorRestoreTimer();
    if (this.liveCursorState.altScreen || !this.liveCursorState.cursorHidden) {
      return;
    }
    this.cursorRestoreTimer = window.setTimeout(() => {
      this.cursorRestoreTimer = 0;
      if (this.liveCursorState.altScreen || !this.liveCursorState.cursorHidden) {
        return;
      }
      this.writeToTerminal(store.getState().terminal.sessionId, SHOW_CURSOR_SEQUENCE);
      this.liveCursorState = { ...this.liveCursorState, cursorHidden: false };
    }, TerminalController.CURSOR_RESTORE_DELAY_MS);
  }

  // handleTerminalOutput stays on the controller because it does the
  // imperative xterm write and appends to the registry's per-session
  // buffer Maps (which are the perf-carveout that justifies the registry
  // existing at all). State-side effects are dispatched as thunks.
  private handleTerminalOutput(payload: TerminalOutputPayload): void {
    const data = decodeBase64Bytes(payload.data);
    this.sessions.appendSessionBuffer(payload.sessionId, data);
    store.dispatch(updateOpenStatusFromOutput(payload.sessionId, decodeTerminalOutput(data)));
    const displayData = filterTerminalDisplayData(data);
    this.sessions.appendDisplayBuffer(payload.sessionId, displayData);
    if (payload.sessionId !== store.getState().terminal.sessionId) {
      return;
    }
    store.dispatch(hideTerminalMessageIfActive(payload.sessionId));
    this.writeToTerminal(payload.sessionId, displayData);
    this.liveCursorState = scanCursorVisibility(this.liveCursorState, displayData);
    this.scheduleCursorRestoreIfStuck();
  }

  // writeToTerminal is the single seam for every xterm write. It tags the write
  // with its source session via the write-source queue and hands xterm the
  // matching completion callback, so terminal query replies fired while xterm
  // parses this chunk route back to sessionId (issue #347). replay marks
  // chunks re-rendered from the saved display buffer, whose stale queries must
  // be consumed without replying (issue #484).
  private writeToTerminal(sessionId: number, data: TerminalWriteData, replay = false): void {
    const terminal = this.terminal;
    if (!terminal) {
      return;
    }
    terminal.write(data, this.writeSources.begin(sessionId, replay));
  }

  layoutCallbacks(): {
    applyLayoutVars: () => void;
    focusTerminalSoon: () => void;
    queueTerminalResize: () => void;
    flushTerminalResize: () => void;
  } {
    return {
      applyLayoutVars: () => {
        this.applyLayoutVars();
      },
      focusTerminalSoon: () => {
        this.focusTerminalSoon();
      },
      queueTerminalResize: this.queueTerminalResize,
      flushTerminalResize: this.flushTerminalResize,
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
      this.runTerminalResize();
    }, 40);
  };

  // flushTerminalResize fits the terminal on the next animation frame
  // and resizes the PTY immediately, bypassing the 40 ms debounce that
  // queueTerminalResize uses to coalesce drag/ResizeObserver bursts.
  // One-shot layout toggles (review/sidebar/debug) call this so the
  // shell sees the new cols before its next prompt redraw — the gap
  // that caused issue #433 (review-open squashes the terminal and only
  // partially un-squashes when closed because the PTY was still on the
  // narrow cols when the next prompt was emitted).
  flushTerminalResize = (): void => {
    window.clearTimeout(this.resizeTimer);
    this.resizeTimer = 0;
    if (this.resizeFrame !== 0) {
      return;
    }
    this.resizeFrame = window.requestAnimationFrame(() => {
      this.resizeFrame = 0;
      this.runTerminalResize();
    });
  };

  private runTerminalResize(): void {
    this.applyLayoutVars();
    this.fitAddon?.fit();
    this.publishTerminalDims();
    const sessionId = store.getState().terminal.sessionId;
    if (sessionId > 0 && this.terminal) {
      ResizeSession(sessionId, this.terminal.cols, this.terminal.rows).catch(noop);
    }
  }

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
    // Keep the now-active node visible in the changed-files tree (#547). Only
    // the diff→tree direction drives this, and it scrolls the tree container,
    // never the diff — so it can't feed back into visibleDiffPath above (which
    // reads the diff/reviewMain scroll position) and re-trigger selection.
    scrollSelectedTreeNodeIntoView(this.treeContainer, path);
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

    const files = pastedFiles(event);
    if (files.length === 0) {
      return;
    }

    event.preventDefault();
    const sessionId = store.getState().terminal.sessionId;
    if (sessionId <= 0) {
      return;
    }
    const paths: string[] = [];
    for (const file of files) {
      const result = await store
        .dispatch(
          sessionApi.endpoints.savePastedFile.initiate({
            sessionId,
            payload: {
              data: await fileToBase64(file),
              mimeType: file.type,
              name: file.name,
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

  writeTerminalBuffer(sessionId: number, chunks: TerminalWriteData[]): void {
    for (const chunk of chunks) {
      this.writeToTerminal(sessionId, chunk, true);
    }
    // Rehydrate live cursor state from the replayed buffer.
    // rebuildTerminalDisplayBuffer already appended `?25h` if the live
    // state would have been "main + hidden", so under normal flow this
    // ends at { altScreen: false, cursorHidden: false }. Scanning keeps
    // the live tracking accurate when a TUI in alt-screen left the
    // buffer in its own intentional state.
    this.liveCursorState = bufferCursorVisibility(chunks);
    this.cancelCursorRestoreTimer();
    // After resetTerminal() + bulk replay, the viewport can settle
    // mid-scrollback because xterm parses write() calls asynchronously on its
    // own timer, so a synchronous scroll here would run before the chunks are
    // laid out. Enqueue an empty write whose completion callback fires only
    // after every replayed chunk has flushed (xterm runs write callbacks in
    // order), then scroll to the live prompt — so switching sessions always
    // lands at the bottom rather than in the middle of history (issue #438).
    this.terminal?.write('', () => {
      this.terminal?.scrollToBottom();
    });
  }
}
