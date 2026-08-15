import type { Locator, Page, Request } from '@playwright/test';

// The headless shim's stand-in for the Wails runtime. ClipboardSetText /
// ClipboardGetText are backed by the backend's clipboard store, so what a spec
// reads here is what the desktop would have put on the host clipboard.
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

// captureInvokes records every backend call the page makes from this point on,
// so a spec can assert both that a call happened and that one did not.
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

// sessionInputs returns the data the frontend sent to one session — the
// observable form of "this keystroke reached the shell".
export function sessionInputs(invokes: InvokeCall[], sessionId: number): string[] {
  return invokes
    .filter((call) => call.method === 'SendSessionInput' && call.args[0] === sessionId)
    .map((call) => call.args[1])
    .filter((data): data is string => typeof data === 'string');
}

// TerminalPane is the xterm surface sessions render into. It owns the
// clipboard-facing interactions around it: what a session emits, what the
// operator selects with the mouse, and what reaches the host clipboard.
export class TerminalPane {
  constructor(private readonly page: Page) {}

  screen(): Locator {
    return this.page.locator('.xterm-screen');
  }

  rows(): Locator {
    return this.page.locator('.xterm-rows');
  }

  // Writes raw bytes to a session as if its PTY had emitted them. btoa is safe
  // only because every byte written through here is < 256.
  async emitOutput(sessionId: number, raw: string): Promise<void> {
    await this.page.evaluate(
      (payload) => {
        const runtime = (window as unknown as { runtime: HeadlessRuntime }).runtime;
        runtime.EventsEmit('terminal-output', {
          sessionId: payload.sessionId,
          data: btoa(payload.raw),
        });
      },
      { sessionId, raw },
    );
  }

  // Clears the screen and prints one line, so the topmost rendered row is known
  // text that a mouse drag can select deterministically.
  async printOnlyLine(sessionId: number, text: string): Promise<void> {
    await this.emitOutput(sessionId, `\x1b[2J\x1b[H${text}`);
  }

  // Drags across the first rendered row, the way an operator selects a URL the
  // session printed.
  async selectFirstRow(): Promise<void> {
    const box = await this.screen().boundingBox();
    if (!box) {
      throw new Error('terminal screen is not rendered');
    }
    // A few pixels below the top of the screen is inside row 0 at any of the
    // font sizes the pane uses.
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

  // Toggling the sidebar provokes a terminal resize, and ResizeSession carries
  // the id of the session that owns the pane — the only place the frontend
  // names it to the backend. Call before printing anything: the toggle reflows
  // the terminal.
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
