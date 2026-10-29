// The user agent is the only platform signal available synchronously at load; a
// Wails round-trip would render one frame with the wrong platform.
export const isMacPlatform =
  typeof navigator !== 'undefined' && /\b(Mac|iPhone|iPad|iPod)\b/.test(navigator.userAgent);

export const isWindowsPlatform =
  typeof navigator !== 'undefined' && /\bWindows\b/.test(navigator.userAgent);
