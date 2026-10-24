import type { FitAddon } from '@xterm/addon-fit';
import type { Terminal } from '@xterm/xterm';
import { noop } from 'erun-kit';

import { ResizeSession } from '../../wailsjs/go/main/App';
import { safeFit } from './terminalFit';

interface ReattachRepaintDeps {
  getTerminal: () => Terminal | null;
  getFitAddon: () => FitAddon | null;
  activeSessionId: () => number;
  afterRestore: () => void;
}

// Kept in sync with the Go side's aiRepaintInputQuiet.
const QUIET_WINDOW_MS = 1500;

// TerminalReattachRepaint forces a reattached TUI to repaint: dtach hands it a
// cleared screen and only a local xterm resize makes Claude redraw. Once the
// screen is blank it fires one shrink/restore cycle.
export class TerminalReattachRepaint {
  private interval = 0;
  private cycling = false;
  private timeout = 0;
  private restoreTo: { sessionId: number; cols: number; rows: number } | null = null;
  private lastInputAt: number | null = null;

  constructor(private readonly deps: ReattachRepaintDeps) {}

  clear(): void {
    if (this.interval !== 0) {
      window.clearInterval(this.interval);
      this.interval = 0;
    }
    if (this.timeout !== 0) {
      window.clearTimeout(this.timeout);
      this.timeout = 0;
    }
    this.cycling = false;
    this.restoreTo = null;
    this.lastInputAt = null;
  }

  // noteInput restores an in-flight cycle immediately, since a reflow mid-edit
  // corrupts the prompt. It does not disarm the poller (the pane's only repaint
  // path); the tick defers through the quiet window instead.
  noteInput(): void {
    this.lastInputAt = Date.now();
    const pending = this.restoreTo;
    if (this.timeout !== 0) {
      window.clearTimeout(this.timeout);
      this.timeout = 0;
    }
    this.restoreTo = null;
    if (pending === null) {
      // A stuck cycling flag would block every later repaint.
      this.cycling = false;
      return;
    }
    this.restore(pending.sessionId, pending.cols, pending.rows);
  }

  schedule(sessionId: number): void {
    this.clear();
    const deadline = performance.now() + 30_000;
    this.interval = window.setInterval(() => {
      if (performance.now() > deadline || this.deps.activeSessionId() !== sessionId) {
        this.clear();
        return;
      }
      if (this.cycling || this.hasVisibleContent()) {
        return;
      }
      if (this.lastInputAt !== null && Date.now() - this.lastInputAt < QUIET_WINDOW_MS) {
        return;
      }
      this.repaintIfBlank(sessionId);
    }, 1300);
  }

  private hasVisibleContent(): boolean {
    const terminal = this.deps.getTerminal();
    if (!terminal) {
      return false;
    }
    const buffer = terminal.buffer.active;
    for (let row = 0; row < terminal.rows; row++) {
      const line = buffer.getLine(buffer.viewportY + row);
      if (line && line.translateToString(true).trim() !== '') {
        return true;
      }
    }
    return false;
  }

  private repaintIfBlank(sessionId: number): void {
    const terminal = this.deps.getTerminal();
    const fitAddon = this.deps.getFitAddon();
    if (!terminal || !fitAddon || this.deps.activeSessionId() !== sessionId) {
      return;
    }
    const { cols, rows } = terminal;
    if (cols <= 0 || rows <= 2) {
      return;
    }
    const shrunk = Math.max(2, rows - Math.ceil(rows / 3));
    this.cycling = true;
    this.restoreTo = { sessionId, cols, rows };
    terminal.resize(cols, shrunk);
    void ResizeSession(sessionId, cols, shrunk).catch(noop);
    this.timeout = window.setTimeout(() => {
      this.timeout = 0;
      this.restoreTo = null;
      this.restore(sessionId, cols, rows);
    }, 650);
  }

  // restore runs from the hold expiring or from noteInput; both clear
  // restoreTo first, so it runs once per cycle.
  private restore(sessionId: number, cols: number, rows: number): void {
    const term = this.deps.getTerminal();
    const fitAddon = this.deps.getFitAddon();
    if (!term || !fitAddon || this.deps.activeSessionId() !== sessionId) {
      this.cycling = false;
      return;
    }
    term.resize(cols, rows);
    safeFit(fitAddon);
    this.deps.afterRestore();
    void ResizeSession(sessionId, term.cols, term.rows).catch(noop);
    this.cycling = false;
  }
}
