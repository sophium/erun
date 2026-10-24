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

// scrollSelectedTreeNodeIntoView moves only when off-screen, so it never fights
// the scrollspy.
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

// diffHunkElements spans every environment section; binary files have no hunks.
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

// stepDiffHunk clamps at either end rather than wrapping.
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

function nextDiffFileHunk(hunks: HTMLElement[], startIndex: number, startPath: string): number {
  for (let i = startIndex + 1; i < hunks.length; i += 1) {
    if (diffHunkFilePath(hunks[i]) !== startPath) {
      return i;
    }
  }
  return -1;
}

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

// VisibleDiffFile carries both halves: selection is env-keyed, DOM lookups use
// the bare path.
export interface VisibleDiffFile {
  envKey: string;
  path: string;
}

// diffSectionTarget is null when either attribute is missing.
function diffSectionTarget(section: HTMLElement): VisibleDiffFile | null {
  const path = section.dataset.path ?? '';
  const envKey = section.dataset.envKey ?? '';
  if (!path || !envKey) {
    return null;
  }
  return { envKey, path };
}

export function visibleDiffFile(
  diffList: HTMLDivElement | null,
  reviewMain: HTMLDivElement | null,
): VisibleDiffFile | null {
  if (!diffList || !reviewMain) {
    return null;
  }
  const sections = Array.from(diffList.querySelectorAll<HTMLElement>('.diff-file[data-path]'));
  if (sections.length === 0) {
    return null;
  }

  const containerRect = reviewMain.getBoundingClientRect();
  const anchor = containerRect.top + 72;
  let closest: VisibleDiffFile | null = null;
  let closestDistance = Number.POSITIVE_INFINITY;

  for (const section of sections) {
    const target = diffSectionTarget(section);
    if (!target) {
      continue;
    }
    const rect = section.getBoundingClientRect();
    if (rect.top <= anchor && rect.bottom > anchor) {
      return target;
    }
    const distance = Math.abs(rect.top - anchor);
    if (distance < closestDistance) {
      closestDistance = distance;
      closest = target;
    }
  }
  return closest;
}
