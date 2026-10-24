import type { Locator, Page, Request } from '@playwright/test';

// Headless shim's Wails runtime; clipboard calls are backed by the backend's clipboard store.
interface HeadlessRuntime {
  EventsEmit: (name: string, ...args: unknown[]) => void;
  ClipboardGetText: () => Promise<string>;
  ClipboardSetText: (text: string) => Promise<boolean>;
}

export interface InvokeCall {
  method: string;
  args: unknown[];
}

export function parseInvoke(req: Request): InvokeCall | null {
  if (req.method() !== 'POST' || !req.url().endsWith('/__erun_invoke')) {
    return null;
  }
  let body: { method?: string; args?: unknown[] } | null = null;
  try {
    body = req.postDataJSON() as { method?: string; args?: unknown[] } | null;
  } catch {
    return null;
  }
  return body?.method ? { method: body.method, args: body.args ?? [] } : null;
}

export function captureInvokes(page: Page): InvokeCall[] {
  const calls: InvokeCall[] = [];
  page.on('request', (req) => {
    const call = parseInvoke(req);
    if (call) {
      calls.push(call);
    }
  });
  return calls;
}

export function sessionInputs(invokes: InvokeCall[], sessionId: number): string[] {
  return invokes
    .filter((call) => call.method === 'SendSessionInput' && call.args[0] === sessionId)
    .map((call) => call.args[1])
    .filter((data): data is string => typeof data === 'string');
}

export class TerminalPane {
  constructor(private readonly page: Page) {}

  screen(): Locator {
    return this.page.locator('.xterm-screen');
  }

  rows(): Locator {
    return this.page.locator('.xterm-rows');
  }

  firstRow(): Locator {
    return this.rows().locator(':scope > div').first();
  }

  // Needs a role/label so screen-reader users know what it is.
  host(): Locator {
    return this.page.getByRole('group', { name: 'Terminal' });
  }

  // Present only with `screenReaderMode: true`.
  accessibilityTree(): Locator {
    return this.page.locator('.xterm-accessibility');
  }

  // btoa is safe only because every byte written here is < 256.
  async emitOutput(sessionId: number, raw: string): Promise<void> {
    await this.emitOutputBatch(sessionId, [raw]);
  }

  // One page.evaluate round trip instead of one CDP round trip per chunk.
  async emitOutputBatch(sessionId: number, rawChunks: string[]): Promise<void> {
    await this.page.evaluate(
      (payload) => {
        const runtime = (window as unknown as { runtime: HeadlessRuntime }).runtime;
        for (const raw of payload.rawChunks) {
          runtime.EventsEmit('terminal-output', {
            sessionId: payload.sessionId,
            data: btoa(raw),
          });
        }
      },
      { sessionId, rawChunks },
    );
  }

  // Leaves the cursor on the row below: on macOS the shell redraws its current line on
  // SIGWINCH, which would erase the printed line if the cursor stayed on row 0.
  async printOnlyLine(sessionId: number, text: string): Promise<void> {
    await this.emitOutput(sessionId, `\x1b[2J\x1b[H${text}\r\n`);
  }

  // Hovers without clicking so async link providers can resolve. Moves off first:
  // xterm's Linkifier ignores a hover on the same buffer cell as last time.
  async hoverFirstRow(): Promise<void> {
    const box = await this.screen().boundingBox();
    if (!box) {
      throw new Error('terminal screen is not rendered');
    }
    await this.page.mouse.move(0, 0);
    await this.page.mouse.move(box.x + 4, box.y + 4);
  }

  // A plain click: xterm activates a link only on a click that selects no text.
  async clickFirstRow(): Promise<void> {
    const box = await this.screen().boundingBox();
    if (!box) {
      throw new Error('terminal screen is not rendered');
    }
    await this.page.mouse.click(box.x + 4, box.y + 4);
  }

  async selectFirstRow(): Promise<void> {
    const box = await this.screen().boundingBox();
    if (!box) {
      throw new Error('terminal screen is not rendered');
    }
    // Inside row 0 at any font size the pane uses.
    const y = box.y + 4;
    await this.page.mouse.move(box.x + 1, y);
    await this.page.mouse.down();
    await this.page.mouse.move(box.x + box.width - 1, y, { steps: 12 });
    await this.page.mouse.up();
  }

  async hostClipboard(): Promise<string> {
    return this.page.evaluate(() => {
      const runtime = (window as unknown as { runtime: HeadlessRuntime }).runtime;
      return runtime.ClipboardGetText();
    });
  }

  async setHostClipboard(text: string): Promise<void> {
    await this.page.evaluate(async (value) => {
      const runtime = (window as unknown as { runtime: HeadlessRuntime }).runtime;
      await runtime.ClipboardSetText(value);
    }, text);
  }

  // Toggling the sidebar resizes the terminal, and ResizeSession carries the session id.
  // Call before printing anything: the toggle reflows the terminal.
  async selectedSessionId(): Promise<number> {
    const waitForResize = this.page
      .waitForRequest((req) => parseInvoke(req)?.method === 'ResizeSession')
      .catch(() => null);
    await this.page.getByRole('button', { name: 'Toggle sidebar' }).click();
    const resize = await waitForResize;
    const id = resize ? parseInvoke(resize)?.args[0] : undefined;
    return typeof id === 'number' ? id : 0;
  }
}
