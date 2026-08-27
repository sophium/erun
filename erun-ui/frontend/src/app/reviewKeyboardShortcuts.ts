// isTypingTarget guards every review-surface key handler: a keydown that
// bubbles up from a text field is the user typing, never a shortcut, no
// matter which key it carries (see ReviewPanel.tsx and
// ReviewDetailDialog.Comments.tsx).
export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  if (target.isContentEditable) {
    return true;
  }
  return target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT';
}

// hasShortcutModifier excludes Cmd/Ctrl/Alt combinations from every review
// shortcut below, so they never shadow a platform or Wails-chrome binding
// that happens to share the same letter.
export function hasShortcutModifier(event: {
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
}): boolean {
  return event.metaKey || event.ctrlKey || event.altKey;
}

export interface ReviewKeyboardShortcut {
  keys: string;
  action: string;
}

// The one list both keyboard-shortcut hints render (DiffList.tsx's per-env
// header and ReviewDetailDialog.tsx's header) -- the diff panel and the
// review's comment threads are two components, but one keyboard model
// (erun-ui/AGENTS.md § "The keyboard model the review surface still owes").
export const REVIEW_KEYBOARD_SHORTCUTS: ReviewKeyboardShortcut[] = [
  { keys: '↓ / ↑', action: 'Next / previous hunk' },
  { keys: '] / [', action: 'Next / previous changed file' },
  { keys: 'S', action: 'Start a review' },
  { keys: '↓ / ↑', action: 'Move between comment threads' },
  { keys: 'R', action: 'Reply to the focused thread' },
  { keys: 'Enter', action: 'Resolve / reopen the focused thread' },
];
