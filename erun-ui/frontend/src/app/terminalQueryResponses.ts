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

  // Query replies route back through the PTY asynchronously, so after the asking
  // tool exits they land on bash's stdin as commands. Swallow them; tools time
  // out to xterm's defaults. DSR stays answered, live parses only.
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
