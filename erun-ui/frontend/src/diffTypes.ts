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

// UIDiffReviewStatus mirrors main.uiDiffReviewStatus. state is a bare string
// (Wails widens it), so switch with a default. 'checking' is frontend-only, and
// 'checking'/'unavailable' differ from 'none', a confirmed absence.
export interface UIDiffReviewStatus {
  state: string;
  platformState?: string;
  reviewId?: string;
  name?: string;
  // Lets "Advance queue" target this review's repository, not any sharing the branch.
  repository?: string;
  queuePosition?: number;
  unresolvedThreads?: number;
  lastFailedBuildId?: string;
  lastMergedBuildId?: string;
  canAdvanceMergeQueue: boolean;
}
