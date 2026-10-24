// Terminal output is untrusted, so javascript:, data: and file: are never activatable.
const ACTIVATABLE_URL_SCHEMES = new Set(['http:', 'https:', 'mailto:']);

export function activatableUrl(raw: string): URL | null {
  try {
    const url = new URL(raw);
    return ACTIVATABLE_URL_SCHEMES.has(url.protocol) ? url : null;
  } catch {
    return null;
  }
}

export const TERMINAL_URL_REGEX =
  /(https?:\/\/[^\s"'!*(){}|\\^<>`]*[^\s"':,.!?{}|\\^~[\]`()<>]|mailto:[^\s"'<>]+[^\s"':,.!?<>])/;
