// TerminalWriteSourceQueue records which PTY session each in-flight xterm write
// belongs to, so terminal query replies (CPR/DSR/DECRQSS) route back to the
// session whose output asked — not whichever session happens to be selected at
// the moment xterm fires the reply handler.
//
// Why a queue and not a single field: xterm parses writes and fires their
// completion callbacks strictly FIFO, and a large write can defer parsing
// across several tasks (xterm yields after a ~12 ms budget and resumes on a
// later setTimeout). If the user switches sessions inside that window, reading
// the currently-selected session at reply time addresses the reply to the wrong
// PTY (issue #347). Pairing every write with begin() and handing its returned
// completion callback to terminal.write(data, callback) keeps the queue head
// aligned with the chunk xterm is parsing right now, so current() yields the
// originating session even across a mid-parse session switch.
export class TerminalWriteSourceQueue {
  private readonly sources: number[] = [];

  // begin records sessionId as the source of the next xterm write and returns
  // the completion callback to hand to terminal.write(data, callback). xterm
  // invokes that callback once the chunk is parsed; FIFO ordering means the
  // first callback to fire belongs to the head entry, so shifting the head
  // keeps the queue aligned. The callback is idempotent so a double-fire can
  // never desync the queue.
  begin(sessionId: number): () => void {
    this.sources.push(sessionId);
    let settled = false;
    return () => {
      if (settled) {
        return;
      }
      settled = true;
      this.sources.shift();
    };
  }

  // current returns the session whose write xterm is parsing right now, or
  // fallback when no write is in flight (e.g. a reply arriving outside any
  // write — preserves the previous "current selection" behaviour as a floor).
  current(fallback: number): number {
    return this.sources[0] ?? fallback;
  }

  // clear drops all pending sources. Call when the terminal is disposed: xterm
  // does not fire completion callbacks for writes abandoned at teardown, so the
  // queue must be reset to avoid carrying a stale head into the next mount.
  clear(): void {
    this.sources.length = 0;
  }
}
