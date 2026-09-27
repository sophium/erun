import * as React from 'react';

// The grace period between a holder releasing the card and the card actually
// closing. It exists so that moving the pointer off the anchor and onto the
// card itself -- a gap the pointer crosses with neither element under it --
// does not dismiss the card. It is not a delay on the close decision itself:
// what the card closes on is the release, and this only lets a hand-off land.
const HOVER_CARD_CLOSE_GRACE_MS = 120;

// useHoverCardOpenState is the open/close behaviour both sidebar hover cards
// (EnvHoverCard, OrchestratorHoverCard) share verbatim: a holder entering opens
// the card immediately, and the last holder leaving closes it after the grace
// period above.
//
// Pointer and focus are tracked as separate holders because a single boolean
// cannot tell "the pointer left" from "focus left" -- both arrive as the same
// release, and the card closed on either one. The gesture that exposed it: the
// pointer resting on a row whose row also had focus (the row is a button, so
// clicking it or tabbing to it focuses it). Focus departing for anywhere else
// released the card while the pointer had not moved, and nothing reopened it --
// reopening takes a fresh mouseenter, and the pointer was already inside. The
// card vanished under a stationary pointer and stayed gone.
//
// So the card closes only once NEITHER holder remains, decided when the grace
// period expires rather than when a release arrives: a leave that is followed
// by the other holder still holding on (or by the pointer reaching the card)
// simply never reaches the close.
export function useHoverCardOpenState(): {
  open: boolean;
  setOpen: (open: boolean) => void;
  hoverEnter: () => void;
  hoverLeave: () => void;
  focusEnter: () => void;
  focusLeave: () => void;
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

  return { open, setOpen, hoverEnter, hoverLeave, focusEnter, focusLeave };
}
