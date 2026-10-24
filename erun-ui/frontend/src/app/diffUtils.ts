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

// visibleDiffFilePaths is shared by the tree and the diff panel so they agree.
export function visibleDiffFilePaths(
  tree: DiffTreeNode[],
  filter: string,
  collapsedDirs: Set<string>,
): Set<string> {
  const visible = visibleDiffTreeNodes(filterDiffTree(tree, filter), collapsedDirs);
  return new Set(visible.filter((node) => node.type === 'file').map((node) => node.path));
}

// chooseSelectedDiffPath keeps the current file while it is still in the diff:
// the diff reloads every 5s, and reselecting files[0] would reset the tree's
// active node and scrollspy on every tick.
export function chooseSelectedDiffPath(diff: DiffResult | null, currentPath: string): string {
  const files = diff?.files ?? [];
  if (files.some((file) => file.path === currentPath)) {
    return currentPath;
  }
  return files[0]?.path ?? '';
}

// diffReviewCommitCount lets an empty scope point at commits another scope shows.
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
