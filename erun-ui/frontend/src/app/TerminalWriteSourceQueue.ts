// Routes terminal query replies (CPR/DSR/DECRQSS) back to the session whose
// output asked. Replies for queries re-parsed from a replayed buffer are
// dropped: the asking tool is long gone, so the reply would land on the live
// shell's stdin as typed junk.
//
// A FIFO queue rather than a single "current session" field, because xterm can
// defer parsing one large write across several tasks; if the user switches
// sessions inside that window, the head still names the session xterm is
// parsing right now, not whatever is selected at reply time.
interface TerminalWriteSource {
  sessionId: number;
  replay: boolean;
}

export class TerminalWriteSourceQueue {
  private readonly sources: TerminalWriteSource[] = [];

  // The returned completion callback is idempotent: xterm can fire it more than
  // once, and a second shift would desync the queue from the chunk being parsed.
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

  current(fallback: number): number {
    return this.sources[0]?.sessionId ?? fallback;
  }

  currentIsReplay(): boolean {
    return this.sources[0]?.replay ?? false;
  }

  // xterm does not fire completion callbacks for writes abandoned at teardown,
  // so the queue must be reset on dispose or a stale head carries into the next mount.
  clear(): void {
    this.sources.length = 0;
  }
}
