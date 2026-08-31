import type { FitAddon } from '@xterm/addon-fit';

// FitAddon.proposeDimensions() floors its own answer at 2 cols / 1 row rather
// than ever reporting zero, so a container that is hidden, mid-layout, or not
// yet painted still proposes a handful of columns -- and fit() applies that
// proposal unconditionally, calling terminal.resize() with it. xterm reflows
// the *viewport* on a resize, but scrollback keeps the wrap it was written
// with, so applying a bad proposal permanently mangles every line already on
// screen. MIN_FIT_COLS/MIN_FIT_ROWS mark the smallest proposal trusted to
// reflect a real layout rather than a measurement caught mid-transition: no
// shell prompt or TUI frame renders legibly below them, and this app's own
// grid never intentionally lays the terminal out that small (MIN_MAIN_PANE_WIDTH
// in state.ts holds <main> at 360px+, an order of magnitude more columns).
export const MIN_FIT_COLS = 10;
export const MIN_FIT_ROWS = 3;

// safeFit resizes the terminal to match its container, but skips the call
// entirely when the proposed dimensions are too small to trust, leaving the
// terminal at its last good size. A skipped fit costs nothing: the container
// stays under a live ResizeObserver, so the next real size change (the
// transition finishing, the container becoming visible) proposes a fresh
// measurement and retries. Returns whether the fit ran, so callers/tests can
// tell a skip from a fit that left dimensions unchanged.
export function safeFit(fitAddon: FitAddon | null | undefined): boolean {
  if (!fitAddon) {
    return false;
  }
  const proposed = fitAddon.proposeDimensions();
  if (!proposed || proposed.cols < MIN_FIT_COLS || proposed.rows < MIN_FIT_ROWS) {
    return false;
  }
  fitAddon.fit();
  return true;
}
