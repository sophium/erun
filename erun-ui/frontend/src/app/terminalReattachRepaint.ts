import type { FitAddon } from '@xterm/addon-fit';
import type { Terminal } from '@xterm/xterm';
import { noop } from 'erun-kit';

import { ResizeSession } from '../../wailsjs/go/main/App';

interface ReattachRepaintDeps {
  getTerminal: () => Terminal | null;
  getFitAddon: () => FitAddon | null;
  activeSessionId: () => number;
  afterRestore: () => void;
}

// TerminalReattachRepaint forces a reattached main-screen TUI (Claude) to repaint.
// dtach hands a reattached client a cleared screen and Claude only redraws on a
// real geometry change that also resizes the LOCAL xterm — a pty-only WINCH does
// not reach it (verified live: the tab stays blank until a keypress). The reattach
// shows the `erun open` bootstrap trace for several seconds before dtach clears it,
// so this polls: it skips while any content is visible (bootstrap OR a rendered
// frame) and, once the screen is actually blank, fires one shrink/restore resize
// cycle. Bash tabs render a prompt on reattach (never blank), so they never trigger
// it; an already-rendered tab is skipped too.
export class TerminalReattachRepaint {
  private interval = 0;
  private cycling = false;

  constructor(private readonly deps: ReattachRepaintDeps) {}

  clear(): void {
    if (this.interval !== 0) {
      window.clearInterval(this.interval);
      this.interval = 0;
    }
    this.cycling = false;
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
    terminal.resize(cols, shrunk);
    void ResizeSession(sessionId, cols, shrunk).catch(noop);
    window.setTimeout(() => {
      const term = this.deps.getTerminal();
      if (!term || this.deps.activeSessionId() !== sessionId) {
        this.cycling = false;
        return;
      }
      term.resize(cols, rows);
      fitAddon.fit();
      this.deps.afterRestore();
      void ResizeSession(sessionId, term.cols, term.rows).catch(noop);
      this.cycling = false;
    }, 650);
  }
}
