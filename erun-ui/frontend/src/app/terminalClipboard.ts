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

// Uploads clipboard files to the active session and returns their remote paths.
// Files with no MIME type still upload; their name carries the extension the
// backend uses for the remote name.
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

// Owns the terminal's OS-clipboard copy/paste behavior. The WebView2 (Windows)
// embedding does not deliver the browser paste event to xterm's hidden
// textarea, so Ctrl+V / right-click / Shift+Insert are intercepted here and the
// OS clipboard is read through the Wails runtime, then fed via xterm's paste
// path (bracketed-paste aware) -> onData -> the session.
export class TerminalClipboard {
  constructor(private readonly deps: TerminalClipboardDeps) {}

  // Returns whether xterm should keep handling the key event.
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

  // A pod-side "press c to copy" affordance emits OSC 52, which xterm swallows
  // unhandled — so the text never leaves the pod, which is the browser-less side
  // of the pair. Handling it writes that text to the host clipboard instead.
  // The sequence is always consumed, rejected reads and malformed payloads
  // included, so pod-controlled bytes never land on screen as junk.
  registerOscClipboardWrites(terminal: Terminal, isReplayParse: () => boolean): IDisposable {
    return terminal.parser.registerOscHandler(OSC_CLIPBOARD_IDENT, (data: string): boolean => {
      // Re-rendering a tab re-parses everything the session ever emitted;
      // re-running the write would clobber the host clipboard with stale text.
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

  // Right-click copies the selection if there is one, otherwise pastes.
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

  // A copy chord with a selection copies it; without one, only the Shift-bearing
  // chord is consumed, so a bare Ctrl+C (or Cmd+C on macOS) still reaches the
  // session — Ctrl+C must stay able to interrupt.
  private handleCopyKey(event: KeyboardEvent): boolean {
    const outcome = terminalCopyOutcome(event, this.deps.getTerminal()?.hasSelection() === true);
    if (outcome === 'copy' && this.copySelection()) {
      event.preventDefault();
      return false;
    }
    return outcome === 'fallthrough';
  }

  // Text paste read straight off the paste event. stopImmediatePropagation keeps
  // xterm from also handling it and double-pasting where the event does arrive.
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

  // Writes the terminal's current selection to the OS clipboard and clears it.
  // Returns false when there is nothing selected, so the caller can fall through
  // (e.g. let Ctrl+C interrupt).
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
