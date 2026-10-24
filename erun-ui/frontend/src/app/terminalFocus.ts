interface TerminalFocusTarget {
  focus: () => void;
}

interface TerminalFocusDeps {
  getTerminal: () => TerminalFocusTarget | null;
  windowIsActive: () => boolean;
  // focusIsFree is false when another control deliberately holds focus.
  focusIsFree: () => boolean;
}

// scheduleTerminalFocus restores focus after a dialog, panel or respawn moved it.
// It must never pull the window forward (respawns are machine-initiated), and
// the guards are re-checked on each attempt since focus can move in between.
export function scheduleTerminalFocus(deps: TerminalFocusDeps): void {
  const focusIfSafe = (): void => {
    if (!deps.windowIsActive()) {
      return;
    }
    if (!deps.focusIsFree()) {
      return;
    }
    deps.getTerminal()?.focus();
  };
  window.setTimeout(() => {
    focusIfSafe();
    window.requestAnimationFrame(() => {
      focusIfSafe();
    });
    window.setTimeout(() => {
      focusIfSafe();
    }, 80);
  }, 0);
}
