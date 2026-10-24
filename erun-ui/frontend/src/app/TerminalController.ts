import { FitAddon } from '@xterm/addon-fit';
import { SerializeAddon } from '@xterm/addon-serialize';
import { type IDisposable, Terminal } from '@xterm/xterm';
import { noop } from 'erun-kit';

import type { TerminalOutputPayload } from '@/types';

import { RepaintSession, ResizeSession, SendSessionInput } from '../../wailsjs/go/main/App';
import { boot } from './bootThunks';
import { decodeBase64Bytes } from './clipboard';
import { readError } from './errors';
import { refreshIdleStatus } from './idleThunks';
import { reconcileSidebarForViewport } from './layoutThunks';
import type { MountElements, TerminalDataDisposable, TerminalWriteData } from './model';
import { showTerminalError } from './notificationThunks';
import { ReviewDiffKeyboardNav } from './reviewDiffKeyboardNav';
import { scrollSelectedTreeNodeIntoView, visibleDiffFile } from './reviewDiffNavigation';
import { diffPathKey, setSelectedDiffPath } from './slices/reviewSlice';
import { loadSavedTerminalScreenReaderMode } from './storage';
import { store } from './store';
import { publishTerminalActivation } from './terminalActivationReadModel';
import {
  bufferCursorVisibility,
  type CursorVisibilityState,
  filterTerminalDisplay,
  MAX_RETAINED_BYTES,
  MAX_RETAINED_LINES,
  scanCursorVisibility,
  SHOW_CURSOR_SEQUENCE,
  TERMINAL_SCROLLBACK,
  trimChunksToBudget,
} from './terminalBuffers';
import { TerminalClipboard } from './terminalClipboard';
import { safeFit } from './terminalFit';
import { scheduleTerminalFocus } from './terminalFocus';
import { applyTerminalLayoutVars } from './terminalLayoutVars';
import { installTerminalLinkHandling } from './terminalLinkHandling';
import { registerTerminalQueryResponseHandlers } from './terminalQueryResponses';
import { TerminalReattachRepaint } from './terminalReattachRepaint';
import { TerminalSessionRegistry } from './TerminalSessionRegistry';
import { plainTerminalText } from './terminalStatus';
import { TerminalSwitchQueue } from './TerminalSwitchQueue';
import { TerminalWailsEvents } from './terminalWailsEvents';
import { TerminalWriteSourceQueue } from './TerminalWriteSourceQueue';
import { thunkExtra } from './thunkExtra';
import { hideTerminalMessageIfActive, updateOpenStatusFromOutput } from './wailsEventThunks';

const REVIEW_DIFF_REFRESH_INTERVAL_MS = 5000;

export class TerminalController {
  readonly sessions = new TerminalSessionRegistry();
  // Source session of each in-flight write, so query replies reach the asking session.
  private readonly writeSources = new TerminalWriteSourceQueue();
  private terminal: Terminal | null = null;
  private fitAddon: FitAddon | null = null;
  private serializeAddon: SerializeAddon | null = null;
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
  private readonly reattachRepaint = new TerminalReattachRepaint({
    getTerminal: () => this.terminal,
    getFitAddon: () => this.fitAddon,
    activeSessionId: () => store.getState().terminal.sessionId,
    afterRestore: () => {
      this.publishTerminalDims();
    },
  });
  private bootStarted = false;
  private terminalDataDisposable: TerminalDataDisposable | null = null;
  private terminalQueryResponseDisposables: IDisposable[] = [];
  private pathLinkProviderDisposable: IDisposable | null = null;
  private clipboardOscDisposable: IDisposable | null = null;
  private readonly wailsEvents = new TerminalWailsEvents();
  private pasteHandler: ((event: ClipboardEvent) => void) | null = null;
  private contextMenuHandler: ((event: MouseEvent) => void) | null = null;
  private readonly reviewDiffKeyboardNav = new ReviewDiffKeyboardNav({
    getDiffList: () => this._diffList,
    getTreeContainer: () => this.treeContainer,
  });
  private reviewDiffKeydownDisposable: (() => void) | null = null;
  // Restores a cursor left hidden on the main screen by a leaked hide sequence;
  // alt-screen TUIs are exempt.
  private liveCursorState: CursorVisibilityState = { altScreen: false, cursorHidden: false };
  private cursorRestoreTimer = 0;
  private static readonly CURSOR_RESTORE_DELAY_MS = 250;
  private readonly switchQueue = new TerminalSwitchQueue({
    behindWrites: (run) => {
      const terminal = this.terminal;
      if (!terminal) {
        return false;
      }
      terminal.write('', run);
      return true;
    },
    appendedCount: (sessionId) => this.sessions.displayAppendedCount(sessionId),
    perform: (from, to, appendedAtRequest) => {
      this.performSwitch(from, to, appendedAtRequest);
    },
  });
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

  // Callback ref: the tree mounts independently of the controller; null on
  // unmount avoids a stale scrollspy target.
  setTreeContainer(element: HTMLDivElement | null): void {
    this.treeContainer = element;
  }

  terminalSize(): { cols: number; rows: number } {
    return { cols: this.terminal?.cols ?? 80, rows: this.terminal?.rows ?? 24 };
  }

  fitTerminal(): void {
    safeFit(this.fitAddon);
    this.publishTerminalDims();
  }

  private publishTerminalDims(): void {
    if (!this.terminalRoot || !this.terminal) {
      return;
    }
    this.terminalRoot.dataset.terminalCols = String(this.terminal.cols);
    this.terminalRoot.dataset.terminalRows = String(this.terminal.rows);
  }

  // WebView2 does not deliver paste events to xterm, so paste reads the OS
  // clipboard via Wails; OSC 52 handles session-to-host copies.
  private installClipboardHandlers(root: HTMLDivElement): void {
    const terminal = this.terminal;
    if (terminal) {
      terminal.attachCustomKeyEventHandler((event: KeyboardEvent): boolean =>
        this.clipboard.handleKeyEvent(event),
      );
      this.clipboardOscDisposable = this.clipboard.registerOscClipboardWrites(terminal, () =>
        this.writeSources.currentIsReplay(),
      );
    }
    this.pasteHandler = (event: ClipboardEvent) => {
      void this.clipboard.handlePaste(event).catch((error: unknown) => {
        store.dispatch(showTerminalError(readError(error)));
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
      screenReaderMode: loadSavedTerminalScreenReaderMode(),
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
    this.serializeAddon = new SerializeAddon();
    this.terminal.loadAddon(this.serializeAddon);
    this.terminal.open(elements.terminalRoot);
    safeFit(this.fitAddon);
    this.publishTerminalDims();
    // The new pane is blank; reset the queue's record of what it holds.
    this.switchQueue.noteRendered(0);
    this.pathLinkProviderDisposable = installTerminalLinkHandling(this.terminal);

    this.installClipboardHandlers(elements.terminalRoot);
    this.reviewDiffKeydownDisposable = this.reviewDiffKeyboardNav.install(elements.reviewMain);

    this.terminalQueryResponseDisposables = registerTerminalQueryResponseHandlers(
      this.terminal,
      // Reply to the session being parsed, not the live selection.
      (data) =>
        SendSessionInput(this.writeSources.current(store.getState().terminal.sessionId), data),
      (error) => {
        store.dispatch(showTerminalError(readError(error)));
      },
      // Replayed buffers must not answer queries again; the reply would land as input.
      () => this.writeSources.currentIsReplay(),
    );
    this.terminalDataDisposable = this.terminal.onData((data) => {
      // Before the write: a pending resize cycle must stand down before input lands.
      this.reattachRepaint.noteInput();
      SendSessionInput(store.getState().terminal.sessionId, data).catch((error: unknown) => {
        store.dispatch(showTerminalError(readError(error)));
      });
    });

    this.resizeObserver = new ResizeObserver(() => {
      this.queueTerminalResize();
    });
    this.resizeObserver.observe(elements.terminalRoot);
    window.addEventListener('resize', this.queueTerminalResize);

    this.wailsEvents.subscribe((payload) => {
      this.handleTerminalOutput(payload);
    });

    if (!this.bootStarted) {
      this.bootStarted = true;
      void store.dispatch(boot());
    }
    this.scheduleIdleStatusPoll(0);
    // A remounted pane is blank and no switch will fill it; re-request the
    // store's session.
    const restoredSessionId = store.getState().terminal.sessionId;
    if (restoredSessionId > 0) {
      this.switchQueue.request(restoredSessionId);
    }

    return () => {
      this.unmountTerminal();
    };
  }

  private unmountTerminal(): void {
    // xterm drops pending write callbacks on dispose, so cancel the queued switch.
    this.switchQueue.cancel();
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
    this.wailsEvents.detach();
    this.pathLinkProviderDisposable?.dispose();
    this.pathLinkProviderDisposable = null;
    window.clearTimeout(this.idleStatusTimer);
    this.reattachRepaint.clear();
    this.stopReviewDiffRefresh();
    this.removeClipboardHandlers();
    this.reviewDiffKeydownDisposable?.();
    this.reviewDiffKeydownDisposable = null;
    this.terminalQueryResponseDisposables = [];
    this.terminal?.dispose();
    this.terminal = null;
    this.fitAddon = null;
    this.serializeAddon = null;
    // xterm drops pending write callbacks on dispose; clear the source queue too.
    this.writeSources.clear();
  }

  private removeClipboardHandlers(): void {
    this.clipboardOscDisposable?.dispose();
    this.clipboardOscDisposable = null;
    const root = this.terminalRoot;
    if (this.pasteHandler && root) {
      root.removeEventListener('paste', this.pasteHandler, true);
    }
    if (this.contextMenuHandler && root) {
      root.removeEventListener('contextmenu', this.contextMenuHandler);
    }
  }

  focusTerminalSoon(): void {
    scheduleTerminalFocus({
      getTerminal: () => this.terminal,
      windowIsActive: () => document.hasFocus(),
      focusIsFree: () => {
        const active = document.activeElement;
        if (!active || active === document.body) {
          return true;
        }
        return this.terminalRoot?.contains(active) ?? false;
      },
    });
  }

  scheduleIdleStatusPoll(delay = 1000): void {
    window.clearTimeout(this.idleStatusTimer);
    this.idleStatusTimer = window.setTimeout(() => {
      void store.dispatch(refreshIdleStatus());
    }, delay);
  }

  // A reset dispatched right after setSessionId is already performed by the
  // switch queue (see resetIsAlreadySatisfied).
  resetTerminal(): void {
    if (this.switchQueue.resetIsAlreadySatisfied(store.getState().terminal.sessionId)) {
      return;
    }
    this.resetPane();
  }

  private resetPane(): void {
    this.terminal?.reset();
    this.terminal?.clear();
    // Record the pane as empty, not session 0: zero is a real switch target.
    this.switchQueue.noteRendered(0);
    this.cancelCursorRestoreTimer();
    this.reattachRepaint.clear();
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
    const text = new TextDecoder().decode(data);
    if (this.sessions.isOpenSession(payload.sessionId)) {
      store.dispatch(updateOpenStatusFromOutput(payload.sessionId, plainTerminalText(text)));
    }
    const display = filterTerminalDisplay(data, text);
    const displayData = display.data;
    this.sessions.appendDisplayBuffer(payload.sessionId, displayData);
    // Inactive sessions are buffered and replayed on their next activation, so
    // the store must never move off a tab the user picked.
    if (payload.sessionId !== store.getState().terminal.sessionId) {
      return;
    }
    store.dispatch(hideTerminalMessageIfActive(payload.sessionId));
    this.writeToTerminal(payload.sessionId, displayData);
    this.liveCursorState = scanCursorVisibility(this.liveCursorState, display.text);
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
    // Resized windows need the same auto-collapse as a narrow launch.
    store.dispatch(reconcileSidebarForViewport());
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

  // Bypasses the resize debounce so the PTY has the new cols before the shell's
  // next prompt redraw.
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
    safeFit(this.fitAddon);
    this.publishTerminalDims();
    const sessionId = store.getState().terminal.sessionId;
    if (sessionId > 0 && this.terminal) {
      ResizeSession(sessionId, this.terminal.cols, this.terminal.rows).catch(noop);
    }
  }

  // resizeActiveSession pushes the pane geometry to the active PTY.
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
    const visible = visibleDiffFile(this._diffList, this.reviewMain);
    if (!visible) {
      return;
    }
    // Selection is env-keyed; only the tree lookup takes the bare path.
    const selected = diffPathKey(visible.envKey, visible.path);
    if (selected === store.getState().review.selectedDiffPath) {
      return;
    }
    store.dispatch(setSelectedDiffPath(selected));
    // Scrolls only the tree container, so it cannot feed back into diff selection.
    scrollSelectedTreeNodeIntoView(this.treeContainer, visible.path);
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

  // requestSessionSwitch queues a switch; dispatches within one task coalesce.
  requestSessionSwitch(sessionId: number): void {
    this.switchQueue.request(sessionId);
  }

  // performSwitch runs inside xterm's write queue, so the capture includes the
  // outgoing session's last output. `to <= 0` only clears the pane.
  private performSwitch(from: number | null, to: number, appendedAtRequest: number): void {
    this.snapshotSession(from ?? 0, appendedAtRequest);
    this.resetPane();
    if (to <= 0) {
      return;
    }
    this.activateSession(to);
    this.resizeActiveSession();
  }

  // snapshotSession captures the outgoing screen and drops buffered output it
  // already contains, so switching back replays only the delta.
  snapshotSession(sessionId: number, appendedAtDispatch = Number.POSITIVE_INFINITY): void {
    if (sessionId <= 0 || !this.terminal || !this.serializeAddon) {
      return;
    }
    this.sessions.captureSnapshot(sessionId, this.serializeAddon.serialize(), appendedAtDispatch);
  }

  // activateSession renders sessionId from its snapshot plus delta, or from a
  // bounded replay of its history.
  activateSession(sessionId: number): void {
    this.switchQueue.noteRendered(sessionId);
    const snapshot = this.sessions.snapshot(sessionId);
    let cursorHidden: boolean;
    // Resolved up front so their count is the switch's cost; excludes the
    // trailing scroll-only write.
    let writes: TerminalWriteData[];
    if (snapshot !== undefined) {
      const delta = this.sessions.displayBuffer(sessionId);
      writes = [snapshot, ...delta];
      // Only the delta is scanned; alt-screen state comes from the live buffer.
      cursorHidden = bufferCursorVisibility(delta).cursorHidden;
    } else {
      const chunks = this.sessions.displayBuffer(sessionId);
      // Main-screen shells replay only what fits in scrollback; alt-screen TUIs need
      // the whole buffer because their initial paint lives at its head.
      const finalState = bufferCursorVisibility(chunks);
      writes = finalState.altScreen
        ? chunks
        : trimChunksToBudget(chunks, MAX_RETAINED_LINES, MAX_RETAINED_BYTES);
      cursorHidden = finalState.cursorHidden;
    }
    for (const chunk of writes) {
      this.writeToTerminal(sessionId, chunk, true);
    }
    publishTerminalActivation(this.terminalRoot, writes.length, snapshot !== undefined);
    this.liveCursorState = {
      cursorHidden,
      altScreen: this.terminal?.buffer.active.type === 'alternate',
    };
    this.cancelCursorRestoreTimer();
    // write() parses asynchronously; scroll once the replay has flushed.
    this.terminal?.write('', () => {
      this.terminal?.scrollToBottom();
    });
    // AI TUIs repaint only on a real geometry change; the backend forces a WINCH
    // for AI/orchestrator sessions and ignores others.
    void RepaintSession(sessionId);
    // A pty-only WINCH misses reattached TUIs; this forces a real resize cycle if
    // the pane stays blank.
    this.reattachRepaint.schedule(sessionId);
    // Replayed history can leave a shell's cursor hidden with no further output.
    this.scheduleCursorRestoreIfStuck();
  }
}
