import { isMacPlatform } from './platform';

export function isTerminalPasteTarget(
  terminalRoot: HTMLDivElement,
  target: EventTarget | null,
): boolean {
  return target instanceof Node && terminalRoot.contains(target);
}

export type TerminalClipboardIntent = 'paste' | 'copy' | 'none';
export type ClipboardPlatform = 'mac' | 'other';

interface ClipboardChords {
  readonly paste: ReadonlySet<string>;
  readonly copy: ReadonlySet<string>;
}

// Which modifier owns the clipboard is a platform contract, not a preference,
// and getting it wrong loses the selection entirely: on macOS the copy chord is
// Cmd+C and Ctrl+C stays the interrupt the shell needs, while Windows/Linux
// follow Windows Terminal (Ctrl+C copies only when something is selected).
//
// Paste is intercepted only for the embedding that fails to deliver the browser
// paste event to xterm's hidden textarea (WebView2). The macOS WebView does
// deliver it, so Cmd+V is deliberately left to that path — it already works and
// it is the path that carries pasted files.
const CLIPBOARD_CHORDS: Record<ClipboardPlatform, ClipboardChords> = {
  mac: {
    paste: new Set<string>(),
    copy: new Set(['meta+c']),
  },
  other: {
    paste: new Set(['ctrl+v', 'ctrl+shift+v', 'shift+Insert']),
    copy: new Set(['ctrl+c', 'ctrl+shift+c']),
  },
};

const hostClipboardPlatform: ClipboardPlatform = isMacPlatform ? 'mac' : 'other';

// Canonical modifier+key signature (modifiers in a fixed order) so chords can be
// matched by lookup instead of a wall of boolean combinations. Meta is part of
// the signature: omitting it reduced Cmd+C to a bare "c", which is why the
// macOS copy chord never reached the copy path.
function keyChord(event: KeyboardEvent): string {
  const parts: string[] = [];
  if (event.ctrlKey) {
    parts.push('ctrl');
  }
  if (event.metaKey) {
    parts.push('meta');
  }
  if (event.shiftKey) {
    parts.push('shift');
  }
  if (event.altKey) {
    parts.push('alt');
  }
  parts.push(event.key.length === 1 ? event.key.toLowerCase() : event.key);
  return parts.join('+');
}

// Classifies a terminal keydown into a clipboard intent for the host platform.
// A 'copy' intent is only the chord match; whether a copy actually happens is
// terminalCopyOutcome's call, so a copy chord with nothing selected can still
// fall through to the session.
export function classifyTerminalClipboardKey(
  event: KeyboardEvent,
  platform: ClipboardPlatform = hostClipboardPlatform,
): TerminalClipboardIntent {
  if (event.type !== 'keydown') {
    return 'none';
  }
  const chord = keyChord(event);
  const chords = CLIPBOARD_CHORDS[platform];
  if (chords.paste.has(chord)) {
    return 'paste';
  }
  if (chords.copy.has(chord)) {
    return 'copy';
  }
  return 'none';
}

export type TerminalCopyOutcome = 'copy' | 'swallow' | 'fallthrough';

// What a copy chord does to the key event. With a selection it copies. Without
// one, a chord that carries Shift (Ctrl+Shift+C) is unambiguously a copy request
// and is still consumed, while a plain copy chord falls through so Ctrl+C keeps
// reaching the session as ^C.
export function terminalCopyOutcome(
  event: KeyboardEvent,
  hasSelection: boolean,
): TerminalCopyOutcome {
  if (hasSelection) {
    return 'copy';
  }
  return event.shiftKey ? 'swallow' : 'fallthrough';
}

// OSC 52 is how a program inside the session asks the terminal to put text on
// the clipboard — what an in-pod agent's "(c to copy)" affordance emits. Its
// payload arrives from the pod, so it is treated as untrusted: the write is
// bounded before it is decoded and anything malformed is dropped rather than
// thrown.
export const OSC_CLIPBOARD_IDENT = 52;

// Far more than the URLs and snippets the affordance exists for, and small
// enough that a runaway sequence cannot build a huge string on the host.
const MAX_CLIPBOARD_TEXT_BYTES = 64 * 1024;
const MAX_CLIPBOARD_BASE64_LENGTH = Math.ceil(MAX_CLIPBOARD_TEXT_BYTES / 3) * 4;

// The host has a single clipboard, so a write aimed only at the X primary
// selection or a cut buffer is ignored: a program mirroring its mouse selection
// into `p` would otherwise overwrite whatever the operator had copied. An empty
// target is the spec's default selection.
const HOST_CLIPBOARD_SELECTIONS = ['c', 's'];

// Decodes an OSC 52 payload ("<selection>;<base64>") into the text to place on
// the host clipboard, or null when there is nothing to write. A read request
// ("?") returns null by design: answering it would let the pod read the host
// clipboard, which is a trust decision the copy direction does not require.
export function parseOscClipboardWrite(data: string): string | null {
  const separator = data.indexOf(';');
  if (separator < 0) {
    return null;
  }
  const selections = data.slice(0, separator);
  const payload = data.slice(separator + 1);
  if (selections && !HOST_CLIPBOARD_SELECTIONS.some((name) => selections.includes(name))) {
    return null;
  }
  if (!payload || payload === '?' || payload.length > MAX_CLIPBOARD_BASE64_LENGTH) {
    return null;
  }
  try {
    return new TextDecoder().decode(decodeBase64Bytes(payload)) || null;
  } catch {
    return null;
  }
}

// pastedFiles returns clipboard files for upload; plain-text paste is left to
// the terminal's own paste path, and files with no MIME type are still returned
// because their name carries the extension the backend uses for the remote name.
export function pastedFiles(event: ClipboardEvent): File[] {
  const items = event.clipboardData?.items;
  if (!items) {
    return [];
  }

  const files: File[] = [];
  for (const item of Array.from(items)) {
    if (item.kind !== 'file') {
      continue;
    }
    const file = item.getAsFile();
    if (file) {
      files.push(file);
    }
  }
  return files;
}

export async function fileToBase64(file: File): Promise<string> {
  const buffer = await file.arrayBuffer();
  return bytesToBase64(new Uint8Array(buffer));
}

export function bytesToBase64(bytes: Uint8Array): string {
  const chunkSize = 0x8000;
  let binary = '';
  for (let index = 0; index < bytes.length; index += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(index, index + chunkSize));
  }
  return window.btoa(binary);
}

export function decodeBase64Bytes(value: string): Uint8Array {
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}
