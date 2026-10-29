import { cssEscape } from './diffUtils';
import {
  diffHunkElements,
  diffHunkFilePath,
  scrollSelectedTreeNodeIntoView,
  stepDiffFile,
  stepDiffHunk,
} from './reviewDiffNavigation';
import { hasShortcutModifier, isTypingTarget } from './reviewKeyboardShortcuts';
import { diffPathKey, setSelectedDiffPath } from './slices/reviewSlice';
import { store } from './store';

export interface ReviewDiffKeyboardNavDeps {
  getDiffList: () => HTMLDivElement | null;
  getTreeContainer: () => HTMLDivElement | null;
}

// hunkSelectionKey builds the env-keyed path from the DOM, matching
// selectedDiffPath.
function hunkSelectionKey(hunk: HTMLElement): string {
  const envKey = hunk.closest<HTMLElement>('[data-env-key]')?.dataset.envKey ?? '';
  const path = diffHunkFilePath(hunk);
  if (!envKey || !path) {
    return '';
  }
  return diffPathKey(envKey, path);
}

export class ReviewDiffKeyboardNav {
  constructor(private readonly deps: ReviewDiffKeyboardNavDeps) {}

  // Anchor: the focused hunk, else the scrollspy's selected file, else start.
  private resolveAnchor(hunks: HTMLElement[]): Element | null {
    const active = document.activeElement;
    if (active && hunks.includes(active as HTMLElement)) {
      return active;
    }
    const selectedPath = store.getState().review.selectedDiffPath;
    if (!selectedPath) {
      return null;
    }
    return hunks.find((hunk) => hunkSelectionKey(hunk) === selectedPath) ?? null;
  }

  // Focus and scroll the hunk, and move the tree selection as scrolling does.
  private focusHunk(hunk: HTMLElement): void {
    hunk.focus();
    hunk.scrollIntoView({ block: 'nearest' });
    const keyed = hunkSelectionKey(hunk);
    if (keyed && keyed !== store.getState().review.selectedDiffPath) {
      store.dispatch(setSelectedDiffPath(keyed));
      scrollSelectedTreeNodeIntoView(this.deps.getTreeContainer(), diffHunkFilePath(hunk));
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

  // Falls back to the first section when nothing is focused.
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

  // A native listener: no ARIA role satisfies jsx-a11y for a scrollable region with
  // a key handler. Skipped while typing or with a Cmd/Ctrl/Alt chord.
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
