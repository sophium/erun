import { spawnSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import { test, expect } from '@playwright/test';

import { TEST_RESULTS_DIRNAME, artifactPath, artifactsRoot } from '../../fixtures/artifacts.js';

// Pins fixtures/artifacts.ts: one artifact root, and a run that cannot use it
// is refused by name before any worker starts. Under tests/harness/ so a change
// here runs the full suite.

const SUITE_DIR = path.join(__dirname, '..', '..');
const PLAYWRIGHT_CLI = path.join(SUITE_DIR, 'node_modules', '@playwright', 'test', 'cli.js');

test.describe('playwright artifact root', () => {
  test('resolves to the suite directory unless the run is told otherwise', () => {
    const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'erun-artifacts-'));
    try {
      withArtifactRoot(undefined, () => {
        // The default puts a pod's frames in the tree the worktree sync carries out.
        expect(artifactsRoot()).toBe(SUITE_DIR);
        expect(artifactPath(TEST_RESULTS_DIRNAME)).toBe(path.join(SUITE_DIR, TEST_RESULTS_DIRNAME));
      });
      withArtifactRoot(scratch, () => {
        expect(artifactPath(TEST_RESULTS_DIRNAME)).toBe(path.join(scratch, TEST_RESULTS_DIRNAME));
      });
    } finally {
      fs.rmSync(scratch, { recursive: true, force: true });
    }
  });

  test('a run whose artifact directory cannot be written is refused by name', () => {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), 'erun-artifacts-'));
    const testResults = path.join(root, TEST_RESULTS_DIRNAME);
    try {
      stageUnusableDirectory(testResults);
      const run = outOfTreeRun(root);
      expect(run.status).not.toBe(0);
      expectRefusalNaming(run.output, testResults);
    } finally {
      fs.rmSync(root, { recursive: true, force: true });
    }
  });

  test('a run whose artifact directory cannot be created is refused by name', () => {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), 'erun-artifacts-'));
    // A file where the directory belongs: the only shape Windows can stage.
    const holder = path.join(root, 'not-a-directory');
    try {
      fs.writeFileSync(holder, '');
      const run = outOfTreeRun(holder);
      expect(run.status).not.toBe(0);
      expectRefusalNaming(run.output, path.join(holder, TEST_RESULTS_DIRNAME));
    } finally {
      fs.rmSync(root, { recursive: true, force: true });
    }
  });
});

// Load the config in a real process; the run must stop before any spec does.
function outOfTreeRun(root: string): { status: number | null; output: string } {
  const run = spawnSync(process.execPath, [PLAYWRIGHT_CLI, 'test', '--list'], {
    cwd: SUITE_DIR,
    env: { ...process.env, ERUN_PLAYWRIGHT_ARTIFACTS_DIR: root },
    encoding: 'utf8',
  });
  return { status: run.status, output: `${run.stdout ?? ''}${run.stderr ?? ''}` };
}

function expectRefusalNaming(output: string, dir: string): void {
  expect(output).toContain(dir);
  expect(output).toContain('ERUN_PLAYWRIGHT_ARTIFACTS_DIR');
}

// Run `body` with the artifact root set to `value`, then restore it.
function withArtifactRoot(value: string | undefined, body: () => void): void {
  const previous = process.env.ERUN_PLAYWRIGHT_ARTIFACTS_DIR;
  if (value === undefined) {
    delete process.env.ERUN_PLAYWRIGHT_ARTIFACTS_DIR;
  } else {
    process.env.ERUN_PLAYWRIGHT_ARTIFACTS_DIR = value;
  }
  try {
    body();
  } finally {
    if (previous === undefined) {
      delete process.env.ERUN_PLAYWRIGHT_ARTIFACTS_DIR;
    } else {
      process.env.ERUN_PLAYWRIGHT_ARTIFACTS_DIR = previous;
    }
  }
}

// Stage an unusable directory: unwritable for a normal user, owned by another
// user for root, and a plain file on Windows.
function stageUnusableDirectory(dir: string): void {
  const uid = typeof process.getuid === 'function' ? process.getuid() : undefined;
  if (uid === undefined) {
    fs.writeFileSync(dir, '');
    return;
  }
  fs.mkdirSync(dir, { recursive: true });
  if (uid === 0) {
    fs.chownSync(dir, 1000, 1000);
    return;
  }
  fs.chmodSync(dir, 0o500);
}
