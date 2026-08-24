// Diff view types, split out of types.ts because that file crossed eslint's
// 500-line max-lines cap. Nothing here changes shape; types.ts re-exports the
// whole module so every existing `from './types'` import keeps working.
export interface DiffResult {
  workingDirectory?: string;
  rawDiff: string;
  summary: DiffSummary;
  files?: DiffFile[];
  tree?: DiffTreeNode[];
  reviewBase?: DiffReviewBase;
  reviewCommits?: DiffCommit[];
  scope?: 'current' | 'commit' | 'all';
  selectedCommit?: string;
  includesWorktree?: boolean;
}

export interface DiffSummary {
  fileCount: number;
  additions: number;
  deletions: number;
}

export interface DiffFile {
  path: string;
  oldPath?: string;
  newPath?: string;
  status: string;
  additions: number;
  deletions: number;
  binary?: boolean;
  hunks?: DiffHunk[];
}

export interface DiffHunk {
  header: string;
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  lines?: DiffLine[];
}

export interface DiffLine {
  kind: 'context' | 'add' | 'delete' | 'meta';
  content: string;
  oldLine?: number;
  newLine?: number;
}

export interface DiffTreeNode {
  name: string;
  path: string;
  parentPath?: string;
  type: 'directory' | 'file';
  depth: number;
  status?: string;
  additions?: number;
  deletions?: number;
}

export interface DiffReviewBase {
  branch?: string;
  commit?: string;
  shortCommit?: string;
}

export interface DiffCommit {
  hash: string;
  shortHash: string;
  subject: string;
  author: string;
  date: string;
}
