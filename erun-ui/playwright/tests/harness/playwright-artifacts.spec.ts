import { spawnSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import { test, expect } from '@playwright/test';

import { TEST_RESULTS_DIRNAME, artifactPath, artifactsRoot } from '../../fixtures/artifacts.js';

// The suite's own artifact root, not an application surface: this file drives
// no desktop flow, so it takes the base `test` rather than fixtures/erunApp.ts
// and boots no backend. It lives under tests/harness/ rather than tests/areas/
// deliberately -- it belongs to no area, and that placement is what makes the
// gate's selection resolver treat a change to it as shared infrastructure
// (erun-common/build_playwright_areas.go) and run the full suite.
//
// What it pins is the contract fixtures/artifacts.ts exists for: where the
// suite's artifacts land is one decision, and a run that cannot use that
// location says so, naming the directory, before any worker starts.

const SUITE_DIR = path.join(__dirname, '..', '..');
const PLAYWRIGHT_CLI = path.join(SUITE_DIR, 'node_modules', '@playwright', 'test', 'cli.js');

test.describe('playwright artifact root', () => {
  test('resolves to the suite directory unless the run is told otherwise', () => {
    const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'erun-artifacts-'));
    try {
      withArtifactRoot(undefined, () => {
        // The default is load-bearing rather than incidental: it is what puts a
        // pod's frames in the tree the worktree sync carries out.
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
    // A file where the directory belongs: the shape every platform can stage,
    // and the only one Windows can -- POSIX modes do not bind there, so this is
    // the arm a Windows run of this suite actually exercises. It is also what a
    // misconfigured root looks like, and it is judged by the nearest directory
    // that does exist, the one Playwright would create the artifact directory
    // under.
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

// Load the config in a real process with this artifact root, which is where the
// check runs: what matters is that the run stops before any spec does, not that
// a helper returns a string.
function outOfTreeRun(root: string): { status: number | null; output: string } {
  const run = spawnSync(process.execPath, [PLAYWRIGHT_CLI, 'test', '--list'], {
    cwd: SUITE_DIR,
    env: { ...process.env, ERUN_PLAYWRIGHT_ARTIFACTS_DIR: root },
    encoding: 'utf8',
  });
  return { status: run.status, output: `${run.stdout ?? ''}${run.stderr ?? ''}` };
}

function expectRefusalNaming(output: string, dir: string): void {
  // The directory, not a spec: naming it is what turns the reported failure --
  // a bare EACCES surfacing from whichever spec wrote first, which reads as a
  // per-branch test failure -- into an actionable one.
  expect(output).toContain(dir);
  // The way out, so the message is not just a diagnosis.
  expect(output).toContain('ERUN_PLAYWRIGHT_ARTIFACTS_DIR');
}

// Run `body` with the artifact root set to `value` (unset when undefined), and
// put back whatever the worker had. The root is read per call, not cached, so
// this exercises the same resolution a real run does.
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

// Stage a directory an artifact root cannot use, in the shape this platform and
// user can actually construct. Without root that is a directory left unwritable
// (the reported symptom). With root, permissions do not bind, so the shape that
// matters is the one root alone can create: a directory owned by another user,
// where the run's own writes would succeed and leave the tree's owner unable to
// clean it up -- the state that outlives the run. On Windows, where neither
// POSIX mode can be staged, a plain file stands in the directory's place; every
// shape is one the suite must refuse rather than write through.
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
