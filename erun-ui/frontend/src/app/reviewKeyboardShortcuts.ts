export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  if (target.isContentEditable) {
    return true;
  }
  return target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT';
}

// hasShortcutModifier keeps shortcuts off platform and Wails-chrome chords.
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

export const REVIEW_KEYBOARD_SHORTCUTS: ReviewKeyboardShortcut[] = [
  { keys: '↓ / ↑', action: 'Next / previous hunk' },
  { keys: '] / [', action: 'Next / previous changed file' },
  { keys: 'S', action: 'Start a review' },
  { keys: '↓ / ↑', action: 'Move between comment threads' },
  { keys: 'R', action: 'Reply to the focused thread' },
  { keys: 'Enter', action: 'Resolve / reopen the focused thread' },
];
