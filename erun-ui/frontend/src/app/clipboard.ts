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

// macOS copies with Cmd+C so Ctrl+C stays the interrupt; Windows/Linux follow
// Windows Terminal. Paste is intercepted only on WebView2, which does not
// deliver paste events to xterm; macOS keeps the native path, which carries files.
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

// Meta must be part of the signature, or Cmd+C reduces to a bare "c".
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

// A 'copy' intent is only the chord match; terminalCopyOutcome decides the copy.
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

// Without a selection, Ctrl+Shift+C is still consumed but plain Ctrl+C reaches
// the session as ^C.
export function terminalCopyOutcome(
  event: KeyboardEvent,
  hasSelection: boolean,
): TerminalCopyOutcome {
  if (hasSelection) {
    return 'copy';
  }
  return event.shiftKey ? 'swallow' : 'fallthrough';
}

// OSC 52 payloads come from the pod and are untrusted: bounded before decoding,
// malformed ones dropped.
export const OSC_CLIPBOARD_IDENT = 52;

const MAX_CLIPBOARD_TEXT_BYTES = 64 * 1024;
const MAX_CLIPBOARD_BASE64_LENGTH = Math.ceil(MAX_CLIPBOARD_TEXT_BYTES / 3) * 4;

// Writes aimed only at the primary selection or cut buffers are ignored so a
// program mirroring its mouse selection cannot overwrite the host clipboard.
const HOST_CLIPBOARD_SELECTIONS = ['c', 's'];

// Read requests ("?") return null: the pod must not read the host clipboard.
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

// Files without a MIME type are kept; the backend derives the remote name from
// the file name.
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
