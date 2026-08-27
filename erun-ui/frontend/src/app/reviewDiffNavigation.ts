import { cssEscape } from './diffUtils';

export function scrollSelectedDiffIntoView(
  diffList: HTMLDivElement | null,
  selectedDiffPath: string,
): void {
  if (!selectedDiffPath || !diffList) {
    return;
  }
  const selector = `[data-path="${cssEscape(selectedDiffPath)}"]`;
  diffList
    .querySelector<HTMLElement>(selector)
    ?.scrollIntoView({ block: 'start', behavior: 'smooth' });
}

// scrollSelectedTreeNodeIntoView keeps the tree's active node visible as the
// diff scrolls, moving it only when it is off-screen so it never fights the
// diff→tree scrollspy or jerks the tree on every scroll tick.
export function scrollSelectedTreeNodeIntoView(
  treeContainer: HTMLDivElement | null,
  selectedDiffPath: string,
): void {
  if (!selectedDiffPath || !treeContainer) {
    return;
  }
  const selector = `[data-path="${cssEscape(selectedDiffPath)}"]`;
  const node = treeContainer.querySelector<HTMLElement>(selector);
  if (!node) {
    return;
  }
  const containerRect = treeContainer.getBoundingClientRect();
  const nodeRect = node.getBoundingClientRect();
  if (nodeRect.top < containerRect.top) {
    node.scrollIntoView({ block: 'start', behavior: 'smooth' });
  } else if (nodeRect.bottom > containerRect.bottom) {
    node.scrollIntoView({ block: 'end', behavior: 'smooth' });
  }
}

// diffHunkElements returns every hunk's focusable scroller (DiffList.tsx's
// `role="region"` divs) in DOM order, spanning every linked environment's
// section. A binary file (DiffList.tsx renders "Binary file changed" instead
// of hunks for one) has none, so it is not a stop on this list -- reachable
// through the changed-files tree or Tab, not through hunk/file stepping.
export function diffHunkElements(diffList: HTMLDivElement | null): HTMLElement[] {
  if (!diffList) {
    return [];
  }
  return Array.from(
    diffList.querySelectorAll<HTMLElement>('[role="region"][aria-label^="Diff for "]'),
  );
}

export function diffHunkFilePath(hunk: HTMLElement | undefined): string {
  return hunk?.closest<HTMLElement>('.diff-file[data-path]')?.dataset.path ?? '';
}

// stepDiffHunk moves one hunk at a time in DOM order, so stepping past a
// file's last hunk lands on the next file's first -- "next/previous hunk"
// and "next/previous changed file" share one underlying order. Clamped, not
// wrapped: a diff reads like a document, not a cyclic tab strip, so stepping
// past either end is a no-op rather than jumping to the other end.
export function stepDiffHunk(
  hunks: HTMLElement[],
  active: Element | null,
  direction: 1 | -1,
): HTMLElement | null {
  if (hunks.length === 0) {
    return null;
  }
  const currentIndex = hunks.indexOf(active as HTMLElement);
  const nextIndex = currentIndex < 0 ? 0 : currentIndex + direction;
  return hunks[nextIndex] ?? null;
}

// nextDiffFileHunk finds the first hunk after startIndex whose file differs
// from startPath -- the next file's first hunk.
function nextDiffFileHunk(hunks: HTMLElement[], startIndex: number, startPath: string): number {
  for (let i = startIndex + 1; i < hunks.length; i += 1) {
    if (diffHunkFilePath(hunks[i]) !== startPath) {
      return i;
    }
  }
  return -1;
}

// previousDiffFileHunk walks back past the current file's own hunks, then
// past the previous file's hunks, landing on that previous file's first one.
function previousDiffFileHunk(hunks: HTMLElement[], startIndex: number, startPath: string): number {
  let i = startIndex - 1;
  while (i >= 0 && diffHunkFilePath(hunks[i]) === startPath) {
    i -= 1;
  }
  if (i < 0) {
    return -1;
  }
  const prevPath = diffHunkFilePath(hunks[i]);
  while (i > 0 && diffHunkFilePath(hunks[i - 1]) === prevPath) {
    i -= 1;
  }
  return i;
}

// stepDiffFile jumps straight to the first hunk of the next (or previous)
// file, skipping over every other hunk of the current file in one press.
export function stepDiffFile(
  hunks: HTMLElement[],
  active: Element | null,
  direction: 1 | -1,
): HTMLElement | null {
  if (hunks.length === 0) {
    return null;
  }
  const currentIndex = hunks.indexOf(active as HTMLElement);
  const startIndex = currentIndex < 0 ? 0 : currentIndex;
  const startPath = diffHunkFilePath(hunks[startIndex]);
  const targetIndex =
    direction > 0
      ? nextDiffFileHunk(hunks, startIndex, startPath)
      : previousDiffFileHunk(hunks, startIndex, startPath);
  return targetIndex < 0 ? null : (hunks[targetIndex] ?? null);
}

export function visibleDiffPath(
  diffList: HTMLDivElement | null,
  reviewMain: HTMLDivElement | null,
): string {
  if (!diffList || !reviewMain) {
    return '';
  }
  const sections = Array.from(diffList.querySelectorAll<HTMLElement>('.diff-file[data-path]'));
  if (sections.length === 0) {
    return '';
  }

  const containerRect = reviewMain.getBoundingClientRect();
  const anchor = containerRect.top + 72;
  let closestPath = '';
  let closestDistance = Number.POSITIVE_INFINITY;

  for (const section of sections) {
    const rect = section.getBoundingClientRect();
    const path = section.dataset.path ?? '';
    if (!path) {
      continue;
    }
    if (rect.top <= anchor && rect.bottom > anchor) {
      return path;
    }
    const distance = Math.abs(rect.top - anchor);
    if (distance < closestDistance) {
      closestDistance = distance;
      closestPath = path;
    }
  }
  return closestPath;
}
