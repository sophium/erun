// TerminalWriteSourceQueue records which PTY session each in-flight xterm write
// belongs to — and whether the write streams live output or replays a saved
// buffer — so terminal query replies (CPR/DSR/DECRQSS) route back to the
// session whose output asked, and stale queries re-parsed from a replayed
// buffer are never answered at all: the asking tool is long gone,
// so the reply would land on the live shell's stdin as typed junk.
//
// Why a queue and not a single field: xterm parses writes and fires their
// completion callbacks strictly FIFO, and a large write can defer parsing
// across several tasks (xterm yields after a ~12 ms budget and resumes on a
// later setTimeout). If the user switches sessions inside that window, reading
// the currently-selected session at reply time addresses the reply to the wrong
// PTY. Pairing every write with begin() and handing its returned
// completion callback to terminal.write(data, callback) keeps the queue head
// aligned with the chunk xterm is parsing right now, so current() yields the
// originating session even across a mid-parse session switch.
interface TerminalWriteSource {
  sessionId: number;
  replay: boolean;
}

export class TerminalWriteSourceQueue {
  private readonly sources: TerminalWriteSource[] = [];

  // begin records sessionId as the source of the next xterm write and returns
  // the completion callback to hand to terminal.write(data, callback). xterm
  // invokes that callback once the chunk is parsed; FIFO ordering means the
  // first callback to fire belongs to the head entry, so shifting the head
  // keeps the queue aligned. The callback is idempotent so a double-fire can
  // never desync the queue. replay marks chunks re-rendered from a saved
  // display buffer rather than streamed live from the PTY.
  begin(sessionId: number, replay = false): () => void {
    this.sources.push({ sessionId, replay });
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
    return this.sources[0]?.sessionId ?? fallback;
  }

  // currentIsReplay reports whether the chunk xterm is parsing right now was
  // replayed from a saved buffer. Outside any write it returns false, matching
  // the live-input assumption the current() fallback makes.
  currentIsReplay(): boolean {
    return this.sources[0]?.replay ?? false;
  }

  // clear drops all pending sources. Call when the terminal is disposed: xterm
  // does not fire completion callbacks for writes abandoned at teardown, so the
  // queue must be reset to avoid carrying a stale head into the next mount.
  clear(): void {
    this.sources.length = 0;
  }
}
