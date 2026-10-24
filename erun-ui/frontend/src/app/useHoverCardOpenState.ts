import * as React from 'react';

// Lets the pointer cross the gap from anchor to card without closing it.
const HOVER_CARD_CLOSE_GRACE_MS = 120;

// Pointer and focus are separate holders: the card closes only when neither
// holds it once the grace period expires, so focus leaving never closes a card
// under a stationary pointer.
export function useHoverCardOpenState(): {
  open: boolean;
  setOpen: (open: boolean) => void;
  hoverEnter: () => void;
  hoverLeave: () => void;
  focusEnter: () => void;
  focusLeave: () => void;
  refuseDismissWhileHeld: (event: { preventDefault: () => void }) => void;
} {
  const [open, setOpen] = React.useState(false);
  const closeTimer = React.useRef(0);
  const held = React.useRef<Record<'hover' | 'focus', boolean>>({ hover: false, focus: false });

  const enter = React.useCallback((holder: 'hover' | 'focus') => {
    held.current[holder] = true;
    window.clearTimeout(closeTimer.current);
    setOpen(true);
  }, []);

  const leave = React.useCallback((holder: 'hover' | 'focus') => {
    held.current[holder] = false;
    window.clearTimeout(closeTimer.current);
    closeTimer.current = window.setTimeout(() => {
      if (!held.current.hover && !held.current.focus) {
        setOpen(false);
      }
    }, HOVER_CARD_CLOSE_GRACE_MS);
  }, []);

  React.useEffect(
    () => () => {
      window.clearTimeout(closeTimer.current);
    },
    [],
  );

  const hoverEnter = React.useCallback(() => {
    enter('hover');
  }, [enter]);
  const hoverLeave = React.useCallback(() => {
    leave('hover');
  }, [leave]);
  const focusEnter = React.useCallback(() => {
    enter('focus');
  }, [enter]);
  const focusLeave = React.useCallback(() => {
    leave('focus');
  }, [leave]);

  // Radix dismisses on focus moving outside without asking the holders; refuse
  // that while a holder remains. Escape and outside pointer-down still close.
  const refuseDismissWhileHeld = React.useCallback((event: { preventDefault: () => void }) => {
    if (held.current.hover || held.current.focus) {
      event.preventDefault();
    }
  }, []);

  return {
    open,
    setOpen,
    hoverEnter,
    hoverLeave,
    focusEnter,
    focusLeave,
    refuseDismissWhileHeld,
  };
}
