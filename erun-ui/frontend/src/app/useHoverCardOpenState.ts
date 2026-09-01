import * as React from 'react';

// useHoverCardOpenState is the open/close behaviour both sidebar hover cards
// (EnvHoverCard, OrchestratorHoverCard) share verbatim: hovering or focusing
// the anchor opens immediately, leaving it closes after a short grace period
// so moving the pointer from the row onto the card doesn't dismiss it.
// Extracted so the two cards share one implementation instead of drifting.
export function useHoverCardOpenState(): {
  open: boolean;
  setOpen: (open: boolean) => void;
  openNow: () => void;
  closeSoon: () => void;
} {
  const [open, setOpen] = React.useState(false);
  const closeTimer = React.useRef(0);
  const openNow = React.useCallback(() => {
    window.clearTimeout(closeTimer.current);
    setOpen(true);
  }, []);
  const closeSoon = React.useCallback(() => {
    window.clearTimeout(closeTimer.current);
    closeTimer.current = window.setTimeout(() => {
      setOpen(false);
    }, 120);
  }, []);
  React.useEffect(
    () => () => {
      window.clearTimeout(closeTimer.current);
    },
    [],
  );
  return { open, setOpen, openNow, closeSoon };
}
