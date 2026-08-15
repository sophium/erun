// The WebView user agent is the only platform signal available synchronously at
// module load; a Wails Environment() round-trip would render one frame with the
// wrong platform first. macOS differs from Windows/Linux in places that must not
// disagree with each other — the titlebar inset that clears the traffic lights,
// and which modifier owns the terminal's clipboard chords — so both read this
// one predicate.
export const isMacPlatform =
  typeof navigator !== 'undefined' && /\b(Mac|iPhone|iPad|iPod)\b/.test(navigator.userAgent);
