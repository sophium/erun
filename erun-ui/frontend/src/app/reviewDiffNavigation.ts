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

// scrollSelectedTreeNodeIntoView keeps the changed-files tree's active node
// visible as the diff is scrolled. It scrolls the tree's own scroll
// container only when the node is currently out of view (scroll-into-view-if-
// needed, nearest edge), so it never fights the diff→tree scrollspy or yanks
// the tree on every scroll tick. Driven solely by the diff→tree direction; it
// acts on the tree container, never on the diff container.
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
