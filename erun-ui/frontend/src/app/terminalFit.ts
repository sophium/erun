import type { FitAddon } from '@xterm/addon-fit';

// proposeDimensions() never reports below 2x1, and a resize to a mid-layout
// proposal permanently mangles scrollback wrap, so smaller proposals are ignored.
export const MIN_FIT_COLS = 10;
export const MIN_FIT_ROWS = 3;

// safeFit skips untrustworthy proposals; the ResizeObserver retries on the next
// size change. Returns whether the fit ran.
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
