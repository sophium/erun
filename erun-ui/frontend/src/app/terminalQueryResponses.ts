import type { IDisposable, Terminal } from '@xterm/xterm';

type TerminalInputSender = (data: string) => Promise<unknown>;
type TerminalInputErrorHandler = (error: unknown) => void;

const ESC = '\x1B';

export function registerTerminalQueryResponseHandlers(
  terminal: Terminal,
  sendInput: TerminalInputSender,
  onError: TerminalInputErrorHandler,
  isReplayParse: () => boolean,
): IDisposable[] {
  const sendResponse = async (data: string): Promise<boolean> => {
    if (!data) {
      return true;
    }
    try {
      await sendInput(data);
    } catch (error: unknown) {
      onError(error);
    }
    return true;
  };

  // xterm.js ships built-in OSC 10/11/12 handlers that answer fg/bg/cursor
  // color queries with the current theme, built-in CSI DA1/DA2 handlers
  // that answer terminal device-attribute probes, and we used to answer
  // DECRQSS (`DCS $ q … ST`) status-string queries here too. All of these
  // replies travel through SendSessionInput -> PTY, which is async — so
  // when the asking process (claude, codex, an interactive ssh, …) exits
  // between query and reply, OR a query reaches xterm on reattach while
  // the session is back at a bare bash prompt, the reply lands on the
  // shell's stdin and bash interprets it as user input. Most visible
  // manifestations: a bare `1;2c1;2c…` after claude exits (DA), and a
  // `1$r0"q␛\` tail at the prompt on reattach (DECRQSS) — bash then tries
  // to run them as commands.
  //
  // Suppress every reply by registering no-op handlers that consume the
  // query and return true so the built-in handler does not run. Querying
  // tools time out (typical timeout ~100ms) and fall back to xterm
  // defaults — the same defaults a hardcoded reply gave anyway. The
  // display strip in terminalBuffers.ts catches partially-consumed tails
  // as a backstop.
  //
  // Cursor-position reports (CSI DSR `n` / DEC DSR `?n`) are the one query
  // we still answer: tools genuinely need the cursor location and there is
  // no sane default to time out to. Those replies still route to the
  // asking session via the writeSources queue (issue #347).
  //
  // …but only for live parses. Query bytes are saved verbatim in the
  // per-session buffer, so re-rendering a tab (setSessionId →
  // terminalDisplayMiddleware → writeTerminalBuffer) re-parses every query a
  // tool ever emitted in that session — BuildKit's tty progress and claude
  // both probe with `ESC[6n`. The asking tool is long gone by then, so the
  // re-answered report lands on the shell's stdin and readline echoes its
  // printable tail as typed junk (`1;64R1;69R…` at the prompt, issue #484).
  // isReplayParse (backed by the writeSources queue) identifies those
  // replayed chunks; their queries are consumed without a reply.
  const suppressQuery = (): boolean => true;

  return [
    terminal.parser.registerOscHandler(10, suppressQuery),
    terminal.parser.registerOscHandler(11, suppressQuery),
    terminal.parser.registerOscHandler(12, suppressQuery),
    terminal.parser.registerCsiHandler({ final: 'c' }, suppressQuery),
    terminal.parser.registerCsiHandler({ prefix: '>', final: 'c' }, suppressQuery),
    terminal.parser.registerDcsHandler({ intermediates: '$', final: 'q' }, suppressQuery),
    terminal.parser.registerCsiHandler({ final: 'n' }, (params) => {
      if (isReplayParse()) {
        return true;
      }
      switch (firstParam(params)) {
        case 5:
          return sendResponse(`${ESC}[0n`);
        case 6:
          return sendResponse(cursorPositionReport(terminal, ''));
        default:
          return true;
      }
    }),
    terminal.parser.registerCsiHandler({ prefix: '?', final: 'n' }, (params) => {
      if (isReplayParse() || firstParam(params) !== 6) {
        return true;
      }
      return sendResponse(cursorPositionReport(terminal, '?'));
    }),
  ];
}

function firstParam(params: (number | number[])[]): number {
  const value = params[0];
  if (Array.isArray(value)) {
    return value[0] ?? 0;
  }
  return value ?? 0;
}

function cursorPositionReport(terminal: Terminal, prefix: string): string {
  const buffer = terminal.buffer.active;
  return `${ESC}[${prefix}${String(buffer.cursorY + 1)};${String(buffer.cursorX + 1)}R`;
}
