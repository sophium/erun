// Owns the hand-off of the shared xterm between sessions. The write barrier
// may fire synchronously (xterm drains immediately after user input) or later;
// nothing here may depend on which. Requests made before the pane moves
// coalesce onto one switch; a pair that returns to the starting tab still runs,
// so its capture is taken. A cleared pane holds null, never 0, because 0 is a
// real switch target.
export class TerminalSwitchQueue {
  // The session the pane actually holds; switches start from this, not from the
  // store's previous session.
  private paneSessionId: number | null = null;
  // The single pending switch target; null (not 0) means none is pending.
  private pendingSessionId: number | null = null;
  // The pane session's append sequence at request time, taken now because the
  // buffer head can move before the switch runs (see captureSnapshot).
  private pendingAppendedAtRequest = 0;

  constructor(
    private readonly options: {
      // Returns false when there is no pane to order against.
      behindWrites: (run: () => void) => boolean;
      appendedCount: (sessionId: number) => number;
      // `to <= 0` clears the pane; `from` is null when it holds nothing.
      perform: (from: number | null, to: number, appendedAtRequest: number) => void;
    },
  ) {}

  // True when a caller's clear is already accounted for: either a pending switch
  // will clear the pane, or a switch already ran this turn (the barrier can fire
  // synchronously) and left the store's session on screen.
  resetIsAlreadySatisfied(sessionId: number): boolean {
    if (this.pendingSessionId !== null) {
      return true;
    }
    return this.paneSessionId === null || this.paneSessionId === sessionId;
  }

  // A non-positive session means the pane was cleared, never "holds 0".
  noteRendered(sessionId: number): void {
    this.paneSessionId = sessionId > 0 ? sessionId : null;
  }

  request(sessionId: number): void {
    if (this.pendingSessionId !== null) {
      this.pendingSessionId = sessionId;
      return;
    }
    this.pendingSessionId = sessionId;
    // Only the first request sets the boundary; moving it later would drop output.
    const from = this.paneSessionId;
    this.pendingAppendedAtRequest = from === null ? 0 : this.options.appendedCount(from);
    const ordered = this.options.behindWrites(() => {
      this.runPending();
    });
    if (!ordered) {
      this.cancel();
    }
  }

  // Drops a switch a teardown made unrunnable so it cannot leak into a later pane.
  cancel(): void {
    this.pendingSessionId = null;
  }

  private runPending(): void {
    const target = this.pendingSessionId;
    this.pendingSessionId = null;
    if (target === null) {
      return;
    }
    this.options.perform(this.paneSessionId, target, this.pendingAppendedAtRequest);
  }
}
