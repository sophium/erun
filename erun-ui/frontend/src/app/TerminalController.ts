import { FitAddon } from '@xterm/addon-fit';
import { type IDisposable, Terminal } from '@xterm/xterm';

import { noop } from '@/lib/utils';
import type { TerminalExitPayload, TerminalOutputPayload } from '@/types';

import { ResizeSession, SendSessionInput } from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { boot, reloadStateAfterEnvironmentChange } from './bootThunks';
import { decodeBase64Bytes } from './clipboard';
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
import { store } from './store';
import {
  bufferCursorVisibility,
  type CursorVisibilityState,
  filterTerminalDisplayData,
  scanCursorVisibility,
  SHOW_CURSOR_SEQUENCE,
} from './terminalBuffers';
import { TerminalClipboard } from './terminalClipboard';
import { applyTerminalLayoutVars } from './terminalLayoutVars';
import { registerTerminalQueryResponseHandlers } from './terminalQueryResponses';
import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { decodeTerminalOutput } from './terminalStatus';
import { TerminalWriteSourceQueue } from './TerminalWriteSourceQueue';
import { thunkExtra } from './thunkExtra';
import {
  handleAIActivity,
  handleAppNotification,
  handleAppStatus,
  handleEnvironmentDeployed,
  handleEnvironmentInitFailed,
  handleEnvironmentInitialized,
  handleEnvStatus,
  handleReconnectLine,
  handleTerminalExit,
  hideTerminalMessageIfActive,
  updateOpenStatusFromOutput,
} from './wailsEventThunks';

const REVIEW_DIFF_REFRESH_INTERVAL_MS = 5000;

// xterm keeps only this many lines of scrollback, so replaying more history than
// this on a session switch is pure parse/render cost that xterm immediately
// discards. Switching to a long-running session therefore replayed multiple MB
// of history — the terminal visibly scrolled through all of it for ~20s before
// landing at the prompt. The replay is capped to this budget below.
const TERMINAL_SCROLLBACK = 5000;
// Replay a little more than the scrollback (long lines wrap into several rows),
// but never more than a hard byte ceiling so a sparse-newline stream can't blow
// the cap. Both bound switch cost to O(scrollback) instead of O(total history).
const MAX_REPLAY_LINES = TERMINAL_SCROLLBACK * 2;
const MAX_REPLAY_BYTES = 2_000_000;

function countNewlines(chunk: TerminalWriteData): number {
  let count = 0;
  if (typeof chunk === 'string') {
    for (const ch of chunk) {
      if (ch === '\n') count++;
    }
    return count;
  }
  for (const byte of chunk) {
    if (byte === 10) count++;
  }
  return count;
}

// trimReplayChunks keeps only the tail of the retained buffer worth replaying —
// enough to fill xterm's scrollback, not the whole session history. Returns the
// original slice reference when nothing needs trimming.
function trimReplayChunks(chunks: TerminalWriteData[]): TerminalWriteData[] {
  let lines = 0;
  let bytes = 0;
  let start = 0;
  for (let i = chunks.length - 1; i >= 0; i--) {
    start = i;
    const chunk = chunks[i];
    if (chunk === undefined) continue;
    lines += countNewlines(chunk);
    bytes += chunk.length;
    if (lines >= MAX_REPLAY_LINES || bytes >= MAX_REPLAY_BYTES) break;
  }
  return start === 0 ? chunks : chunks.slice(start);
}

export class TerminalController {
  readonly sessions = new TerminalSessionRegistry();
  // Tracks the source session of each in-flight xterm write so terminal query
  // replies route back to the asking session, not the currently-selected one.
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
  private environmentDeployedOff: (() => void) | null = null;
  private environmentsChangedOff: (() => void) | null = null;
  private aiActivityOff: (() => void) | null = null;
  private envStatusOff: (() => void) | null = null;
  private pasteHandler: ((event: ClipboardEvent) => void) | null = null;
  private contextMenuHandler: ((event: MouseEvent) => void) | null = null;
  // When the active session ends in "main screen + cursor hidden" with no
  // further output, restore the cursor so an unmatched hide leaked by
  // `erun open`, helm, kubectl, or a remote-side spinner doesn't strand the
  // prompt with no visible cursor. Alt-screen TUIs are exempt by design.
  private liveCursorState: CursorVisibilityState = { altScreen: false, cursorHidden: false };
  private cursorRestoreTimer = 0;
  private static readonly CURSOR_RESTORE_DELAY_MS = 250;
  private readonly clipboard = new TerminalClipboard({
    getTerminal: () => this.terminal,
    getTerminalRoot: () => this.terminalRoot,
    focusTerminalSoon: () => {
      this.focusTerminalSoon();
    },
  });

  constructor() {
    thunkExtra.controller = this;
  }

  get terminalPane(): HTMLElement | null {
    return this._terminalPane;
  }

  get reviewView(): HTMLElement | null {
    return this._reviewView;
  }

  get diffList(): HTMLDivElement | null {
    return this._diffList;
  }

  // A callback ref rather than part of mount(): the changed-files tree is only
  // rendered while its section is open, so it mounts and unmounts independently
  // of the one-time controller mount — passing null on unmount avoids a stale
  // reference that would break the diff→tree scrollspy.
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

  private subscribeEnvironmentLifecycleEvents(): void {
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
    this.environmentDeployedOff = EventsOn(
      'environment-deployed',
      (payload: EnvironmentInitializedPayload) => {
        void store.dispatch(handleEnvironmentDeployed(payload));
      },
    );
  }

  private subscribeWailsEvents(): void {
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
    this.subscribeEnvironmentLifecycleEvents();
    this.environmentsChangedOff = EventsOn('environments-changed', () => {
      void store.dispatch(reloadStateAfterEnvironmentChange());
    });
    this.aiActivityOff = EventsOn('ai-activity', (payload: AIActivityPayload) => {
      store.dispatch(handleAIActivity(payload));
    });
    this.envStatusOff = EventsOn('env-status', (payload: EnvStatusPayload) => {
      store.dispatch(handleEnvStatus(payload));
    });
  }

  // Wires the OS-clipboard copy/paste handlers: the WebView2 embedding does not
  // deliver the browser paste event to xterm, so Ctrl+V / right-click / paste
  // route through TerminalClipboard, which reads the OS clipboard via the Wails
  // runtime and feeds it through xterm's paste path.
  private installClipboardHandlers(root: HTMLDivElement): void {
    this.terminal?.attachCustomKeyEventHandler((event: KeyboardEvent): boolean =>
      this.clipboard.handleKeyEvent(event),
    );
    this.pasteHandler = (event: ClipboardEvent) => {
      void this.clipboard.handlePaste(event).catch((error: unknown) => {
        store.dispatch(showTerminalMessage(readError(error)));
      });
    };
    root.addEventListener('paste', this.pasteHandler, true);
    this.contextMenuHandler = (event: MouseEvent) => {
      this.clipboard.handleContextMenu(event);
    };
    root.addEventListener('contextmenu', this.contextMenuHandler);
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
      scrollback: TERMINAL_SCROLLBACK,
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

    this.installClipboardHandlers(elements.terminalRoot);

    this.terminalQueryResponseDisposables = registerTerminalQueryResponseHandlers(
      this.terminal,
      // Reply to the session whose output xterm is parsing right now, not the
      // live selection: the user may have switched sessions during a deferred
      // parse, which would misroute the reply.
      (data) =>
        SendSessionInput(this.writeSources.current(store.getState().terminal.sessionId), data),
      (error) => {
        store.dispatch(showTerminalMessage(readError(error)));
      },
      // Suppress replies to queries re-parsed from a replayed display buffer:
      // the asking tool consumed the live reply long ago, so a second reply
      // would land on the session's shell as typed input.
      () => this.writeSources.currentIsReplay(),
    );
    this.terminalDataDisposable = this.terminal.onData((data) => {
      SendSessionInput(store.getState().terminal.sessionId, data).catch((error: unknown) => {
        store.dispatch(showTerminalMessage(readError(error)));
      });
    });

    this.resizeObserver = new ResizeObserver(() => {
      this.queueTerminalResize();
    });
    this.resizeObserver.observe(elements.terminalRoot);
    window.addEventListener('resize', this.queueTerminalResize);

    this.subscribeWailsEvents();

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
    if (this.contextMenuHandler && this.terminalRoot) {
      this.terminalRoot.removeEventListener('contextmenu', this.contextMenuHandler);
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
      'environmentDeployedOff',
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
    applyTerminalLayoutVars({
      reviewView: this._reviewView,
      terminalPane: this._terminalPane,
    });
  }

  queueTerminalResize = (): void => {
    window.clearTimeout(this.resizeTimer);
    this.resizeTimer = window.setTimeout(() => {
      this.runTerminalResize();
    }, 40);
  };

  // One-shot layout toggles (review/sidebar/debug) call this so the shell sees
  // the new cols before its next prompt redraw, rather than waiting on the
  // debounce queueTerminalResize uses to coalesce drag bursts. Otherwise an
  // opened review squashes the terminal and only partially un-squashes on
  // close, because the PTY was still on the narrow cols when the next prompt
  // was emitted.
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

  // resizeActiveSession pushes the current terminal geometry to the active PTY.
  // Called when the active session changes so a session spawned at a default
  // size (e.g. an orchestrator) is resized to the pane instead of rendering its
  // UI at the wrong width.
  resizeActiveSession(): void {
    this.runTerminalResize();
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
    // Keep the now-active node visible in the changed-files tree. Only
    // the diff→tree direction drives this, and it scrolls the tree container,
    // never the diff — so it can't feed back into visibleDiffPath above (which
    // reads the diff/reviewMain scroll position) and re-trigger selection.
    scrollSelectedTreeNodeIntoView(this.treeContainer, path);
  }

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

  writeTerminalBuffer(sessionId: number, chunks: TerminalWriteData[]): void {
    // Rehydrate live cursor tracking from the full buffer (a cheap scan, not a
    // render); its final alt-screen verdict also decides how much to replay.
    const finalState = bufferCursorVisibility(chunks);
    // Main-screen shells: replay only the tail that fills xterm's scrollback —
    // replaying the whole history just scroll-renders lines xterm then discards
    // (the ~20s scroll-through this cap fixed). Alt-screen TUIs (claude/codex):
    // the visible frame is drawn by cursor-addressed redraws whose alt-screen
    // enter (`?1049h`) + initial paint live in the buffer HEAD, so trimming the
    // head would leave those redraws on a blank main screen — a black pane.
    // Alt-screen has no scrollback, so a full replay carries no scroll-through
    // cost; replay it whole.
    const replayChunks = finalState.altScreen ? chunks : trimReplayChunks(chunks);
    for (const chunk of replayChunks) {
      this.writeToTerminal(sessionId, chunk, true);
    }
    this.liveCursorState = finalState;
    this.cancelCursorRestoreTimer();
    // xterm parses write() calls asynchronously, so a synchronous scroll would
    // run before the replayed chunks are laid out. The empty write's callback
    // fires only after every replayed chunk has flushed, so switching sessions
    // lands at the live prompt rather than mid-history.
    this.terminal?.write('', () => {
      this.terminal?.scrollToBottom();
    });
  }
}
