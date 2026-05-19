import type { IDisposable, Terminal } from '@xterm/xterm';

type TerminalInputSender = (data: string) => Promise<unknown>;
type TerminalInputErrorHandler = (error: unknown) => void;

const ESC = '\x1B';
const ST = `${ESC}\\`;

export function registerTerminalQueryResponseHandlers(
  terminal: Terminal,
  sendInput: TerminalInputSender,
  onError: TerminalInputErrorHandler,
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

  // OSC 10/11/12 (fg/bg/cursor color) queries are deliberately unanswered:
  // the Wails+PTY reply path is too slow for fire-and-forget queries, so
  // the reply leaks into the shell's stdin and gets run as a command.
  return [
    terminal.parser.registerCsiHandler({ final: 'c' }, (params) => {
      if (firstParam(params) > 0) {
        return true;
      }
      return sendResponse(`${ESC}[?1;2c`);
    }),
    terminal.parser.registerCsiHandler({ prefix: '>', final: 'c' }, (params) => {
      if (firstParam(params) > 0) {
        return true;
      }
      return sendResponse(`${ESC}[>0;276;0c`);
    }),
    terminal.parser.registerCsiHandler({ final: 'n' }, (params) => {
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
      if (firstParam(params) !== 6) {
        return true;
      }
      return sendResponse(cursorPositionReport(terminal, '?'));
    }),
    terminal.parser.registerDcsHandler({ intermediates: '$', final: 'q' }, (data) => {
      return sendResponse(statusStringReport(terminal, data));
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

function statusStringReport(terminal: Terminal, data: string): string {
  switch (data) {
    case '"q':
      return `${ESC}P1$r0"q${ST}`;
    case '"p':
      return `${ESC}P1$r61;1"p${ST}`;
    case 'r':
      return `${ESC}P1$r1;${String(terminal.rows)}r${ST}`;
    case 'm':
      return `${ESC}P1$r0m${ST}`;
    case ' q':
      return `${ESC}P1$r${String(cursorStyleReport(terminal))} q${ST}`;
    default:
      return `${ESC}P0$r${ST}`;
  }
}

function cursorStyleReport(terminal: Terminal): number {
  const style = terminal.options.cursorStyle;
  const blink = terminal.options.cursorBlink === true;
  if (style === 'underline') {
    return blink ? 3 : 4;
  }
  if (style === 'bar') {
    return blink ? 5 : 6;
  }
  return blink ? 1 : 2;
}
