// Scheme allowlist for terminal-rendered URLs (plain http(s) matches and OSC 8
// hyperlinks alike). A terminal renders untrusted output -- an agent or a
// build log can print anything -- so `javascript:`, `data:`, and `file:` must
// never be activatable: `file:` from a pod-side tab is exactly the host/pod
// path confusion #1354 exists to prevent, wearing a URL instead of a path.
const ACTIVATABLE_URL_SCHEMES = new Set(['http:', 'https:', 'mailto:']);

export function activatableUrl(raw: string): URL | null {
  try {
    const url = new URL(raw);
    return ACTIVATABLE_URL_SCHEMES.has(url.protocol) ? url : null;
  } catch {
    return null;
  }
}

// A regex combining plain http(s) URLs (xterm's own web-links default,
// widened here to also carry mailto so both need only one registered
// provider) for WebLinksAddon's custom urlRegex option.
export const TERMINAL_URL_REGEX =
  /(https?:\/\/[^\s"'!*(){}|\\^<>`]*[^\s"':,.!?{}|\\^~[\]`()<>]|mailto:[^\s"'<>]+[^\s"':,.!?<>])/;
