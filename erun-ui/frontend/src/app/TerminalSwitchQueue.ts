// Owns the hand-off of the shared xterm between sessions: which session the
// pane holds, and which switch is due.
//
// xterm parses write() asynchronously, on a macrotask of its own. A reader
// clicking from one terminal tab to another and straight back delivers two
// setSessionId dispatches inside that window, and each one taken on its own
// would snapshot, reset and activate: the first activation's writes are then
// still queued when the second switch resets the pane, so they parse into the
// freshly-reset buffer and the landed tab shows the tab it was switched from
// appended above its own screen. The pane stops corresponding to any single
// session, and it stays that way -- the buffer is cleared only ahead of the
// writes that repaint it.
//
// So a request is not a switch. While one is pending, every further request
// replaces its target: the switch that runs moves from what the pane is really
// showing to what the store last asked for, and a pair that comes back to the
// tab it started on coalesces to from === target and does nothing at all. The
// barrier is the ordering guarantee and the coalescing window in one: it is
// invoked from xterm's own queue once every chunk queued ahead of it has been
// parsed, so it can never fire before a dispatch that arrived in the same task,
// or in a microtask of it.
export class TerminalSwitchQueue {
  // The session whose screen the shared xterm currently holds. A switch starts
  // from this rather than from the session the store named before it, because a
  // switch only becomes real when it runs: requests delivered before the pane
  // has moved have to coalesce onto what is actually on screen, not onto each
  // other.
  private paneSessionId = 0;
  // The target of the switch a dispatch asked for. At most one switch is
  // pending: a request arriving before the barrier fires replaces this target
  // instead of queueing a second switch behind the first. `null` -- not 0,
  // which is a real target (a close clears the pane) -- is what "no switch is
  // pending" means.
  private pendingSessionId: number | null = null;

  constructor(
    private readonly options: {
      // Orders `run` behind every write already issued for the session being
      // left. Returns false when there is no pane to order it against.
      behindWrites: (run: () => void) => boolean;
      // Hands the pane from `from` to `to`. `to <= 0` clears the pane.
      perform: (from: number, to: number) => void;
    },
  ) {}

  // True while a switch is due. A reset issued now would be a duplicate of the
  // one that switch performs, and would run ahead of the screen it captures --
  // see TerminalController.resetTerminal.
  pending(): boolean {
    return this.pendingSessionId !== null;
  }

  // Records the session the pane was rendered with.
  noteRendered(sessionId: number): void {
    this.paneSessionId = sessionId;
  }

  request(sessionId: number): void {
    if (this.pendingSessionId !== null) {
      this.pendingSessionId = sessionId;
      return;
    }
    this.pendingSessionId = sessionId;
    const ordered = this.options.behindWrites(() => {
      this.runPending();
    });
    if (!ordered) {
      this.cancel();
    }
  }

  // Drops a switch that a teardown made unrunnable, so it cannot be carried
  // into a later pane and suppress that pane's first reset.
  cancel(): void {
    this.pendingSessionId = null;
  }

  private runPending(): void {
    const target = this.pendingSessionId;
    this.pendingSessionId = null;
    if (target === null) {
      return;
    }
    const from = this.paneSessionId;
    // A pair that returned to the tab it started on: the pane never left it, so
    // there is no screen to capture and none to clear. The tab the pair clicked
    // through keeps its buffered screen to replay on its first real display.
    if (from === target) {
      return;
    }
    this.options.perform(from, target);
  }
}
