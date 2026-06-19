export function isTerminalPasteTarget(
  terminalRoot: HTMLDivElement,
  target: EventTarget | null,
): boolean {
  return target instanceof Node && terminalRoot.contains(target);
}

// pastedFiles returns every file on the clipboard, regardless of MIME type.
// Plain-text paste arrives as `kind === 'string'` items, never `'file'`, so it
// is left untouched here and flows through to the terminal's normal text paste.
// Files with an empty MIME type (common for non-image files) still carry their
// extension in the name, so the backend can derive a sensible remote filename.
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
