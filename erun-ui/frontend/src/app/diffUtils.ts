import type { DiffLine, DiffResult, DiffTreeNode } from '@/types';

export function filterDiffTree(nodes: DiffTreeNode[], filter: string): DiffTreeNode[] {
  if (!filter) {
    return nodes;
  }
  const matchingPaths = new Set<string>();
  const nodesByPath = new Map(nodes.map((node) => [node.path, node]));
  for (const node of nodes.filter((item) => item.type === 'file')) {
    if (!node.path.toLowerCase().includes(filter)) {
      continue;
    }
    matchingPaths.add(node.path);
    let parentPath = node.parentPath ?? '';
    while (parentPath) {
      matchingPaths.add(parentPath);
      parentPath = nodesByPath.get(parentPath)?.parentPath ?? '';
    }
  }
  return nodes.filter((node) => matchingPaths.has(node.path));
}

export function visibleDiffTreeNodes(
  nodes: DiffTreeNode[],
  collapsedDiffDirs: Set<string>,
): DiffTreeNode[] {
  const nodesByPath = new Map(nodes.map((node) => [node.path, node]));
  return nodes.filter((node) => {
    let parentPath = node.parentPath ?? '';
    while (parentPath) {
      if (collapsedDiffDirs.has(parentPath)) {
        return false;
      }
      parentPath = nodesByPath.get(parentPath)?.parentPath ?? '';
    }
    return true;
  });
}

// visibleDiffFilePaths is the subset the changed-files tree and the diff panel
// must both honour, so an active filter or collapsed directory can never make
// the two panels disagree.
export function visibleDiffFilePaths(
  tree: DiffTreeNode[],
  filter: string,
  collapsedDirs: Set<string>,
): Set<string> {
  const visible = visibleDiffTreeNodes(filterDiffTree(tree, filter), collapsedDirs);
  return new Set(visible.filter((node) => node.type === 'file').map((node) => node.path));
}

export function chooseSelectedDiffPath(diff: DiffResult | null, currentPath: string): string {
  const files = diff?.files ?? [];
  if (files.some((file) => file.path === currentPath)) {
    return currentPath;
  }
  return files[0]?.path ?? '';
}

// diffReviewCommitCount tells an empty-state render whether a different scope
// would show commits this one doesn't -- the diff result carries reviewCommits
// regardless of which scope fetched it, so an empty "current" scope can still
// point at history waiting to be reviewed instead of asserting a flat "no
// changes" (see DiffEmptyState).
export function diffReviewCommitCount(diff: DiffResult | null | undefined): number {
  return diff?.reviewCommits?.length ?? 0;
}

export function compactDiffError(message: string): string {
  if (message.includes('unknown tool "diff"')) {
    return 'Runtime MCP does not expose diff yet. Refresh after deploy finishes.';
  }
  return message;
}

export function diffLineMark(kind: DiffLine['kind']): string {
  if (kind === 'add') {
    return '+';
  }
  if (kind === 'delete') {
    return '-';
  }
  return '';
}

export function cssEscape(value: string): string {
  if ('CSS' in window && typeof window.CSS.escape === 'function') {
    return window.CSS.escape(value);
  }
  return value.split('"').join('\\"');
}
