import assert from 'node:assert/strict';

import { test } from 'vitest';

import type { DiffResult } from '@/types';

import { chooseSelectedDiffPath } from './diffUtils';

function diffOf(...paths: string[]): DiffResult {
  return {
    rawDiff: '',
    workingDirectory: '/seed',
    summary: { fileCount: paths.length, additions: 0, deletions: 0 },
    files: paths.map((path) => ({
      path,
      status: 'modified',
      additions: 1,
      deletions: 0,
      binary: false,
      hunks: [],
    })),
    tree: [],
    scope: 'current',
    includesWorktree: true,
  } as unknown as DiffResult;
}

const FILES = ['pkg/f00.ts', 'pkg/f01.ts', 'pkg/f29.ts'];

// The panel reloads every environment's diff on a 5s timer, so this runs on
// every tick. Re-selecting files[0] unconditionally is what reset the
// changed-files tree's active node five seconds after a scroll put it there:
// the tree marks `aria-current` only for the selected file, and once the diff
// stopped scrolling nothing re-derived the selection.
test('chooseSelectedDiffPath keeps the current file so a periodic reload does not reset the selection', () => {
  assert.equal(chooseSelectedDiffPath(diffOf(...FILES), 'pkg/f29.ts'), 'pkg/f29.ts');
});

test('chooseSelectedDiffPath falls back to the first file when the current one left the diff', () => {
  assert.equal(chooseSelectedDiffPath(diffOf(...FILES), 'pkg/gone.ts'), 'pkg/f00.ts');
});

test('chooseSelectedDiffPath selects the first file when nothing is selected yet', () => {
  assert.equal(chooseSelectedDiffPath(diffOf(...FILES), ''), 'pkg/f00.ts');
});

test('chooseSelectedDiffPath has nothing to select in an empty diff', () => {
  assert.equal(chooseSelectedDiffPath(diffOf(), 'pkg/f29.ts'), '');
  assert.equal(chooseSelectedDiffPath(null, 'pkg/f29.ts'), '');
});
