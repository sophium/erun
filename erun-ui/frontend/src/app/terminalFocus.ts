interface TerminalFocusTarget {
  focus: () => void;
}

interface TerminalFocusDeps {
  getTerminal: () => TerminalFocusTarget | null;
  // windowIsActive is false exactly when another application is frontmost.
  windowIsActive: () => boolean;
}

// scheduleTerminalFocus restores focus to the terminal after something moved it
// -- closing a dialog, toggling a panel, respawning a tab.
//
// It only ever RESTORES focus inside a window that is already active; it must
// never PULL the window forward. Several of its callers are machine-initiated
// rather than user-initiated: a tab respawn fires whenever a session drops, and
// a session drops on every env stop, idle-stop and pod replacement. Without the
// guard erun yanks the desktop in front of whatever the operator was actually
// using, mid-sentence (#1338).
//
// The guard is re-checked before each of the three attempts rather than once up
// front. The later two exist to land after a re-render, and focus can leave the
// window in between -- checking only at schedule time would let the 80ms
// attempt steal focus the user had already moved elsewhere.
export function scheduleTerminalFocus(deps: TerminalFocusDeps): void {
  const focusIfWindowActive = (): void => {
    if (!deps.windowIsActive()) {
      return;
    }
    deps.getTerminal()?.focus();
  };
  window.setTimeout(() => {
    focusIfWindowActive();
    window.requestAnimationFrame(() => {
      focusIfWindowActive();
    });
    window.setTimeout(() => {
      focusIfWindowActive();
    }, 80);
  }, 0);
}
