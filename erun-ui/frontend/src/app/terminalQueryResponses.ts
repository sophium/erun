import type { IDisposable, Terminal } from '@xterm/xterm';

type TerminalInputSender = (data: string) => Promise<unknown>;
type TerminalInputErrorHandler = (error: unknown) => void;

const ESC = '\x1B';
const ST = `${ESC}\\`;

const foregroundColor = 'rgb:ffff/ffff/ffff';
const backgroundColor = 'rgb:0000/0000/0000';
const cursorColor = foregroundColor;

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
    terminal.parser.registerOscHandler(10, (data) => {
      const response = colorReportData(10, data);
      return response === null ? false : sendResponse(response);
    }),
    terminal.parser.registerOscHandler(11, (data) => {
      const response = colorReportData(11, data);
      return response === null ? false : sendResponse(response);
    }),
    terminal.parser.registerOscHandler(12, (data) => {
      const response = colorReportData(12, data);
      return response === null ? false : sendResponse(response);
    }),
  ];
}

function firstParam(params: (number | number[])[]): number {
  const value = params[0];
  if (Array.isArray(value)) {
    return value[0] || 0;
  }
  return value || 0;
}

function cursorPositionReport(terminal: Terminal, prefix: string): string {
  const buffer = terminal.buffer.active;
  return `${ESC}[${prefix}${buffer.cursorY + 1};${buffer.cursorX + 1}R`;
}

function statusStringReport(terminal: Terminal, data: string): string {
  switch (data) {
    case '"q':
      return `${ESC}P1$r0"q${ST}`;
    case '"p':
      return `${ESC}P1$r61;1"p${ST}`;
    case 'r':
      return `${ESC}P1$r1;${terminal.rows}r${ST}`;
    case 'm':
      return `${ESC}P1$r0m${ST}`;
    case ' q':
      return `${ESC}P1$r${cursorStyleReport(terminal)} q${ST}`;
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

function colorReportData(start: number, data: string): string | null {
  if (data.split(';').some((slot) => slot !== '?')) {
    return null;
  }
  return colorReports(start, data).join('');
}

function colorReports(start: number, data: string): string[] {
  const reports: string[] = [];
  const slots = data.split(';');
  for (let index = 0; index < slots.length; index++) {
    if (slots[index] !== '?') {
      continue;
    }
    const colorIndex = start + index;
    const color = specialColor(colorIndex);
    if (color) {
      reports.push(`${ESC}]${colorIndex};${color}${ST}`);
    }
  }
  return reports;
}

function specialColor(index: number): string {
  switch (index) {
    case 10:
      return foregroundColor;
    case 11:
      return backgroundColor;
    case 12:
      return cursorColor;
    default:
      return '';
  }
}
