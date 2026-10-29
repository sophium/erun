import type { IDisposable, Terminal } from '@xterm/xterm';

import { SendSessionInput } from '../../wailsjs/go/main/App';
import { ClipboardGetText, ClipboardSetText } from '../../wailsjs/runtime/runtime';
import { sessionApi } from './api/sessionApi';
import {
  classifyTerminalClipboardKey,
  fileToBase64,
  isTerminalPasteTarget,
  OSC_CLIPBOARD_IDENT,
  parseOscClipboardWrite,
  pastedFiles,
  terminalCopyOutcome,
} from './clipboard';
import { readError } from './errors';
import { showTerminalError } from './notificationThunks';
import { store } from './store';

export interface TerminalClipboardDeps {
  getTerminal: () => Terminal | null;
  getTerminalRoot: () => HTMLDivElement | null;
  focusTerminalSoon: () => void;
}

// Files without a MIME type still upload; the name carries the extension.
async function uploadPastedFiles(sessionId: number, files: File[]): Promise<string[]> {
  const paths: string[] = [];
  for (const file of files) {
    const result = await store
      .dispatch(
        sessionApi.endpoints.savePastedFile.initiate({
          sessionId,
          payload: {
            data: await fileToBase64(file),
            mimeType: file.type,
            name: file.name,
          },
        }),
      )
      .unwrap();
    if (result.path) {
      paths.push(result.path);
    }
  }
  return paths;
}

// WebView2 does not deliver paste events to xterm's textarea, so paste keys are
// intercepted and the clipboard is read through the Wails runtime.
export class TerminalClipboard {
  constructor(private readonly deps: TerminalClipboardDeps) {}

  handleKeyEvent(event: KeyboardEvent): boolean {
    const intent = classifyTerminalClipboardKey(event);
    if (intent === 'paste') {
      event.preventDefault();
      void this.pasteFromClipboard();
      return false;
    }
    if (intent === 'copy') {
      return this.handleCopyKey(event);
    }
    return true;
  }

  // OSC 52 writes go to the host clipboard. The sequence is always consumed so
  // pod-controlled bytes never render as junk.
  registerOscClipboardWrites(terminal: Terminal, isReplayParse: () => boolean): IDisposable {
    return terminal.parser.registerOscHandler(OSC_CLIPBOARD_IDENT, (data: string): boolean => {
      // A replay parse must not clobber the clipboard with stale text.
      if (isReplayParse()) {
        return true;
      }
      const text = parseOscClipboardWrite(data);
      if (text !== null) {
        void ClipboardSetText(text).catch((error: unknown) => {
          this.reportError(error);
        });
      }
      return true;
    });
  }

  handleContextMenu(event: MouseEvent): void {
    event.preventDefault();
    if (this.deps.getTerminal()?.hasSelection()) {
      this.copySelection();
    } else {
      void this.pasteFromClipboard();
    }
  }

  async handlePaste(event: ClipboardEvent): Promise<void> {
    const root = this.deps.getTerminalRoot();
    if (!root || !isTerminalPasteTarget(root, event.target)) {
      return;
    }
    const files = pastedFiles(event);
    if (files.length === 0) {
      this.pasteClipboardText(event);
      return;
    }
    event.preventDefault();
    await this.pasteFiles(files);
  }

  // Without a selection only the Shift chord is consumed, so Ctrl+C still interrupts.
  private handleCopyKey(event: KeyboardEvent): boolean {
    const outcome = terminalCopyOutcome(event, this.deps.getTerminal()?.hasSelection() === true);
    if (outcome === 'copy' && this.copySelection()) {
      event.preventDefault();
      return false;
    }
    return outcome === 'fallthrough';
  }

  // stopImmediatePropagation prevents xterm from double-pasting.
  private pasteClipboardText(event: ClipboardEvent): void {
    const text = event.clipboardData?.getData('text/plain') ?? '';
    if (!text) {
      return;
    }
    event.preventDefault();
    event.stopImmediatePropagation();
    this.deps.getTerminal()?.paste(text);
    this.deps.focusTerminalSoon();
  }

  private async pasteFiles(files: File[]): Promise<void> {
    const sessionId = store.getState().terminal.sessionId;
    if (sessionId <= 0) {
      return;
    }
    const paths = await uploadPastedFiles(sessionId, files);
    if (paths.length === 0) {
      return;
    }
    await SendSessionInput(sessionId, `${paths.join(' ')} `);
    this.deps.focusTerminalSoon();
  }

  private async pasteFromClipboard(): Promise<void> {
    try {
      const text = await ClipboardGetText();
      if (text) {
        this.deps.getTerminal()?.paste(text);
        this.deps.focusTerminalSoon();
      }
    } catch (error: unknown) {
      this.reportError(error);
    }
  }

  private copySelection(): boolean {
    const text = this.deps.getTerminal()?.getSelection() ?? '';
    if (!text) {
      return false;
    }
    void ClipboardSetText(text).catch((error: unknown) => {
      this.reportError(error);
    });
    this.deps.getTerminal()?.clearSelection();
    return true;
  }

  private reportError(error: unknown): void {
    store.dispatch(showTerminalError(readError(error)));
  }
}
