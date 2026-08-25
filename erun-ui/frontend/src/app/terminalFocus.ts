interface TerminalFocusTarget {
  focus: () => void;
}

interface TerminalFocusDeps {
  getTerminal: () => TerminalFocusTarget | null;
  // windowIsActive is false exactly when another application is frontmost.
  windowIsActive: () => boolean;
  // focusIsFree is false when some other control deliberately holds focus --
  // an input the user clicked into, a button they tabbed to. Restoring focus
  // to the terminal is only ever correct when nothing else is holding it.
  focusIsFree: () => boolean;
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
  const focusIfSafe = (): void => {
    // Cross-application: never PULL the window forward (#1338).
    if (!deps.windowIsActive()) {
      return;
    }
    // Within the window: never take focus off a control the user deliberately
    // put it on. A restore exists to undo focus that an action DESTROYED (a
    // dialog closing removes the focused node, so focus falls back to body),
    // not to overrule a live choice. Without this the 80ms attempt lands after
    // the user has already clicked into something else and yanks the caret to
    // the terminal, which reads as the app stealing focus even though the
    // window never changed.
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
