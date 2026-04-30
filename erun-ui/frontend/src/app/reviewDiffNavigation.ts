import { cssEscape } from './diffUtils';

export function scrollSelectedDiffIntoView(diffList: HTMLDivElement | null, selectedDiffPath: string): void {
  if (!selectedDiffPath || !diffList) {
    return;
  }
  const selector = `[data-path="${cssEscape(selectedDiffPath)}"]`;
  diffList.querySelector<HTMLElement>(selector)?.scrollIntoView({ block: 'start', behavior: 'smooth' });
}

export function visibleDiffPath(diffList: HTMLDivElement | null, reviewMain: HTMLDivElement | null): string {
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
    const path = section.dataset.path || '';
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
