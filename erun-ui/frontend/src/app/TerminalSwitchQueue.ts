// Owns the hand-off of the shared xterm between sessions: which session the
// pane holds, and which switch is due.
//
// xterm parses write() asynchronously, but not always: `WriteBuffer.write` runs
// `_innerWrite` synchronously -- draining every queued chunk and firing their
// callbacks on the spot -- when its queue is empty and the user has typed since
// the last drain (`_didUserInput`). A real keystroke sets that flag, so the
// barrier below can fire inside the dispatch that armed it. It may also fire a
// microtask or a macrotask later. Nothing here may depend on which.
//
// What the barrier buys is unchanged either way: it runs once every chunk
// queued ahead of it has been parsed. That is what makes a switch safe to
// perform -- the outgoing session's screen is complete when it is captured, and
// the incoming activation's writes are ordered behind the outgoing session's.
//
// So a request is not a switch. While one is pending, every further request
// replaces its target: two dispatches delivered before the pane moved coalesce
// onto a single switch that moves from what the pane really holds to what the
// store last asked for. A pair that comes back to the tab it started on settles
// on from === target and is performed like any other switch: it captures that
// tab's screen and re-activates it, which keeps the capture a later switch away
// would otherwise be the only chance to take, and the pane still ends on the
// tab the pair clicked to.
//
// The pane's ownership is tracked as `paneSessionId`, and `null` means the pane
// holds nothing. A cleared pane is recorded as holding nothing rather than as
// holding session 0: zero is a real switch target (a close clears the pane), so
// stamping the pane with it would say the pane holds a session it does not.
export class TerminalSwitchQueue {
  // The session whose screen the shared xterm currently holds, or null when the
  // pane is blank. A switch starts from this rather than from the session the
  // store named before it, because a switch only becomes real when it runs:
  // requests delivered before the pane has moved have to coalesce onto what is
  // actually on screen, not onto each other.
  private paneSessionId: number | null = null;
  // The target of the switch a dispatch asked for. At most one switch is
  // pending: a request arriving before the barrier fires replaces this target
  // instead of queueing a second switch behind the first. `null` -- not 0,
  // which is a real target (a close clears the pane) -- is what "no switch is
  // pending" means.
  private pendingSessionId: number | null = null;
  // The append sequence the pane's own session stood at when this switch was
  // asked for, and the boundary the snapshot it takes has to name: output that
  // arrives for that session after this point is not written to the pane (the
  // store no longer names it), so it is not in the snapshot and is the delta
  // the capture has to keep. Taken here rather than when the switch runs,
  // because the buffer's head can move under the boundary in between -- see
  // captureSnapshot.
  private pendingAppendedAtRequest = 0;

  constructor(
    private readonly options: {
      // Orders `run` behind every write already issued for the session being
      // left. Returns false when there is no pane to order it against.
      behindWrites: (run: () => void) => boolean;
      // The session's append sequence number now -- see
      // pendingAppendedAtRequest.
      appendedCount: (sessionId: number) => number;
      // Hands the pane from `from` to `to`, capturing `from`'s screen up to
      // `appendedAtRequest`. `to <= 0` clears the pane. `from` is null when the
      // pane holds nothing to capture.
      perform: (from: number | null, to: number, appendedAtRequest: number) => void;
    },
  ) {}

  // True when the pane is already in the state a caller's clear would produce,
  // so performing it would only destroy a screen that is right.
  //
  // The two tests below are each half of one answer, and between them they do
  // not care where the barrier landed. A switch still due will clear the pane
  // itself, before it activates what the store names -- clearing now would run
  // ahead of the snapshot that switch needs. And a switch that already ran in
  // this same turn left the pane holding exactly what the store names, so a
  // clear now would wipe the render it just produced. Reading the first alone
  // is the bug: a barrier that fired synchronously, inside the dispatch, leaves
  // nothing pending by the time the caller's next statement runs, and the
  // second test is what still recognizes that case.
  resetIsAlreadySatisfied(sessionId: number): boolean {
    if (this.pendingSessionId !== null) {
      return true;
    }
    return this.paneSessionId === null || this.paneSessionId === sessionId;
  }

  // Records the session the pane was rendered with. A non-positive session
  // means the pane was cleared, which is "holds nothing", never "holds 0".
  noteRendered(sessionId: number): void {
    this.paneSessionId = sessionId > 0 ? sessionId : null;
  }

  request(sessionId: number): void {
    if (this.pendingSessionId !== null) {
      this.pendingSessionId = sessionId;
      return;
    }
    this.pendingSessionId = sessionId;
    // On the first request only: a request that replaces a pending target does
    // not re-open the window, and moving the boundary later would drop output
    // that arrived since the switch was asked for.
    const from = this.paneSessionId;
    this.pendingAppendedAtRequest = from === null ? 0 : this.options.appendedCount(from);
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
    this.options.perform(this.paneSessionId, target, this.pendingAppendedAtRequest);
  }
}
