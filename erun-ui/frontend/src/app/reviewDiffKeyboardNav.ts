import { cssEscape } from './diffUtils';
import {
  diffHunkElements,
  diffHunkFilePath,
  scrollSelectedTreeNodeIntoView,
  stepDiffFile,
  stepDiffHunk,
} from './reviewDiffNavigation';
import { hasShortcutModifier, isTypingTarget } from './reviewKeyboardShortcuts';
import { setSelectedDiffPath } from './slices/reviewSlice';
import { store } from './store';

export interface ReviewDiffKeyboardNavDeps {
  getDiffList: () => HTMLDivElement | null;
  getTreeContainer: () => HTMLDivElement | null;
}

// ReviewDiffKeyboardNav owns the diff panel's keyboard model
// (erun-ui/AGENTS.md § "The keyboard model the review surface still owes"):
// next/previous hunk, next/previous changed file, and starting a review for
// the focused environment section. Split out of TerminalController (which
// installs it on the scrollable diff region, the same way it installs
// TerminalClipboard on the terminal root) purely to keep that file under its
// line budget -- this is app state/DOM wiring, not a second controller.
export class ReviewDiffKeyboardNav {
  constructor(private readonly deps: ReviewDiffKeyboardNavDeps) {}

  // "Where keyboard navigation starts from": the hunk that already has DOM
  // focus when one of ours does, otherwise the hunk belonging to the
  // scrollspy's current selectedDiffPath (so the first press continues from
  // what the user is already looking at), otherwise null -- stepDiffHunk/
  // stepDiffFile both treat that as "start of the list".
  private resolveAnchor(hunks: HTMLElement[]): Element | null {
    const active = document.activeElement;
    if (active && hunks.includes(active as HTMLElement)) {
      return active;
    }
    const selectedPath = store.getState().review.selectedDiffPath;
    if (!selectedPath) {
      return null;
    }
    return hunks.find((hunk) => diffHunkFilePath(hunk) === selectedPath) ?? null;
  }

  // Moving focus by keyboard must leave a visible trail: the target hunk is
  // both focused (so :focus-visible renders its ring) and scrolled into view,
  // and the changed-files tree/selection follow it exactly the way scrolling
  // the diff by mouse already drives them (TerminalController's own
  // updateSelectedDiffPathFromScroll).
  private focusHunk(hunk: HTMLElement): void {
    hunk.focus();
    hunk.scrollIntoView({ block: 'nearest' });
    const path = diffHunkFilePath(hunk);
    if (path && path !== store.getState().review.selectedDiffPath) {
      store.dispatch(setSelectedDiffPath(path));
      scrollSelectedTreeNodeIntoView(this.deps.getTreeContainer(), path);
    }
  }

  focusAdjacentHunk(direction: 1 | -1): void {
    const hunks = diffHunkElements(this.deps.getDiffList());
    const target = stepDiffHunk(hunks, this.resolveAnchor(hunks), direction);
    if (target) {
      this.focusHunk(target);
    }
  }

  focusAdjacentFile(direction: 1 | -1): void {
    const hunks = diffHunkElements(this.deps.getDiffList());
    const target = stepDiffFile(hunks, this.resolveAnchor(hunks), direction);
    if (target) {
      this.focusHunk(target);
    }
  }

  // startReviewForFocusedEnv resolves the "Start a review" button that
  // belongs to the currently keyboard-focused environment section (falling
  // back to the first section when nothing is focused yet, the common
  // single-environment case) and activates it -- reusing
  // StartReviewFromDiffAction's own click handler rather than duplicating
  // the dialog-opening logic here.
  startReviewForFocusedEnv(): void {
    const diffList = this.deps.getDiffList();
    if (!diffList) {
      return;
    }
    const hunks = diffHunkElements(diffList);
    const anchor = this.resolveAnchor(hunks);
    const envKey =
      anchor?.closest<HTMLElement>('[data-env-key]')?.dataset.envKey ??
      diffList.querySelector<HTMLElement>('[data-env-key]')?.dataset.envKey;
    if (!envKey) {
      return;
    }
    const header = diffList.querySelector<HTMLElement>(`[data-env-key="${cssEscape(envKey)}"]`);
    header?.querySelector<HTMLButtonElement>('[data-review-action="start-review"]')?.click();
  }

  // install wires a native `keydown` listener on the diff panel's scrollable
  // region -- not a JSX onKeyDown -- for the same reason
  // ReviewDetailDialog.Comments.tsx's comment-thread list uses a delegated
  // native listener: jsx-a11y's non-interactive-element rules have no ARIA
  // role that satisfies both "needs a role for its handler" and "should not
  // have a handler for its role" at once for a scrollable `role="region"`
  // container, so this mirrors TerminalController's installClipboardHandlers
  // shape (a native listener on a DOM node the caller owns) instead of
  // fighting that with a role that doesn't fit. Guarded the same way on
  // every key: no shortcut fires while a text field has focus or a
  // Cmd/Ctrl/Alt chord is held, so typing and platform/Wails-chrome bindings
  // are never shadowed. Returns the disposer.
  install(root: HTMLDivElement): () => void {
    const handler = (event: KeyboardEvent): void => {
      if (isTypingTarget(event.target) || hasShortcutModifier(event)) {
        return;
      }
      switch (event.key) {
        case 'ArrowDown':
          event.preventDefault();
          this.focusAdjacentHunk(1);
          return;
        case 'ArrowUp':
          event.preventDefault();
          this.focusAdjacentHunk(-1);
          return;
        case ']':
          event.preventDefault();
          this.focusAdjacentFile(1);
          return;
        case '[':
          event.preventDefault();
          this.focusAdjacentFile(-1);
          return;
        case 's':
        case 'S':
          event.preventDefault();
          this.startReviewForFocusedEnv();
          return;
        default:
          return;
      }
    };
    root.addEventListener('keydown', handler);
    return () => {
      root.removeEventListener('keydown', handler);
    };
  }
}
