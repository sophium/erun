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

// UIDiffReviewStatus mirrors main.uiDiffReviewStatus's JSON shape. state is
// one of 'checking' | 'unavailable' | 'none' | 'open' | 'ready' | 'blocked' |
// 'failed' | 'merging' | 'merged' | 'closed' -- bare `string`, not a literal
// union, to match the Wails binding, which widens the Go string constants
// (see EnvironmentType above), so callers switch over it with a default case
// rather than assuming the union is exhaustive. 'checking' is set locally by
// diffReviewStatusSlice while a DiffReviewStatus call is in flight; the Go
// side never returns it. 'checking' and 'unavailable' are the honest
// not-yet-known states -- distinct from 'none', a confirmed answer (no live
// review for this branch pair) -- so the chip never renders an absence as a
// fact it hasn't actually established.
export interface UIDiffReviewStatus {
  state: string;
  platformState?: string;
  reviewId?: string;
  name?: string;
  queuePosition?: number;
  unresolvedThreads?: number;
  lastFailedBuildId?: string;
  lastMergedBuildId?: string;
  canAdvanceMergeQueue: boolean;
}
