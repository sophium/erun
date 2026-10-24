// Routes terminal query replies to the session whose output asked, dropping
// replies to replayed output. A FIFO because xterm may parse one write across
// several tasks while the selection changes.
interface TerminalWriteSource {
  sessionId: number;
  replay: boolean;
}

export class TerminalWriteSourceQueue {
  private readonly sources: TerminalWriteSource[] = [];

  // Idempotent: xterm can fire the callback more than once.
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

  // xterm skips callbacks for writes abandoned at teardown, so reset on dispose.
  clear(): void {
    this.sources.length = 0;
  }
}
