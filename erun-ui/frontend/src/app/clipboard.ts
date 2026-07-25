export function isTerminalPasteTarget(
  terminalRoot: HTMLDivElement,
  target: EventTarget | null,
): boolean {
  return target instanceof Node && terminalRoot.contains(target);
}

export type TerminalClipboardIntent = 'paste' | 'copy' | 'none';

// Ctrl+V, Ctrl+Shift+V, and Shift+Insert paste; each requires exactly those
// modifiers (no stray Alt/Ctrl/Shift), matching Windows Terminal.
const PASTE_CHORDS = new Set(['ctrl+v', 'ctrl+shift+v', 'shift+Insert']);

// Canonical modifier+key signature (modifiers in a fixed order) so paste chords
// can be matched by lookup instead of a wall of boolean combinations.
function keyChord(event: KeyboardEvent): string {
  const parts: string[] = [];
  if (event.ctrlKey) {
    parts.push('ctrl');
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

// Classifies a terminal keydown into a clipboard intent. Copy is Ctrl+C or
// Ctrl+Shift+C (never with Alt); the caller decides whether a copy actually
// happens based on the current selection so Ctrl+C can still fall through to ^C.
export function classifyTerminalClipboardKey(event: KeyboardEvent): TerminalClipboardIntent {
  if (event.type !== 'keydown') {
    return 'none';
  }
  if (PASTE_CHORDS.has(keyChord(event))) {
    return 'paste';
  }
  if (event.ctrlKey && !event.altKey && event.key.toLowerCase() === 'c') {
    return 'copy';
  }
  return 'none';
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
  const binary = window.atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}
