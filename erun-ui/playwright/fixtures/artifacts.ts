import * as fs from 'node:fs';
import * as path from 'node:path';

// Where the suite leaves everything a human reads afterwards: Playwright's own
// per-test output (`outputDir`), the HTML report, and the frames specs capture
// by hand. One root owns all three, so ERUN_PLAYWRIGHT_ARTIFACTS_DIR moves them
// together and no writer can keep using a location the others have left.
//
// The default is the suite directory, which is deliberate: the worktree sync
// carries a pod's frames out to a reviewing orchestrator, so an in-environment
// run has to leave them there (see visualFrames.ts).
//
// The override exists for runs whose tree is not theirs to write to. Both
// in-container gate paths set it (the erun-devops Dockerfile's test stage and
// scripts/repro-gate-contention.sh): the latter bind-mounts an environment's
// own worktree over /src and runs as root, so a report written into the default
// location comes back owned by uid 0 -- which the environment user cannot
// remove, and which fails every later run in that environment rather than only
// the run that wrote it.
export const TEST_RESULTS_DIRNAME = 'test-results';
export const PLAYWRIGHT_REPORT_DIRNAME = 'playwright-report';

const SUITE_DIR = path.join(__dirname, '..');

export function artifactsRoot(): string {
  const override = (process.env.ERUN_PLAYWRIGHT_ARTIFACTS_DIR ?? '').trim();
  return override === '' ? SUITE_DIR : path.resolve(override);
}

export function artifactPath(relative: string): string {
  return path.join(artifactsRoot(), relative);
}

// Why a run cannot use one of its artifact directories, or null when it can.
//
// The failure this pre-empts is the one an unreadable artifact directory used
// to produce: Playwright's reporter cannot replace it and a spec's screenshot
// cannot open it, so the run dies deep inside a spec with a bare EACCES that
// names no cause and suggests no action -- while the directory itself is the
// whole story. Checking both names up front costs nothing and keeps the report
// about the directory rather than about whichever spec happened to write first.
//
// A directory that does not exist yet is fine as long as its parent can be
// written: Playwright creates it. Only an existing-but-unusable directory, or
// a parent that cannot hold one, is reported.
export function artifactDirectoryProblem(dir: string): string | null {
  const uid = typeof process.getuid === 'function' ? process.getuid() : undefined;
  const who = uid === undefined ? 'this run' : `uid ${uid}`;
  let stat: fs.Stats;
  try {
    stat = fs.statSync(dir);
  } catch {
    const parent = path.dirname(dir);
    if (directoryWritable(parent)) {
      return null;
    }
    return (
      `${dir} does not exist and ${parent} is not writable by ${who}, so the suite cannot create it. ` +
      'A root-privileged container run against this worktree leaves its parent owned by uid 0 (the gate ' +
      'arrangement scripts/repro-gate-contention.sh mirrors bind-mounts the tree over /src and runs as root): ' +
      `remove it from a container that can (docker run --rm -u 0 -v ${parent}:${parent} rm -rf ${dir}), or point ` +
      'ERUN_PLAYWRIGHT_ARTIFACTS_DIR at a directory outside the repository.'
    );
  }
  if (!stat.isDirectory()) {
    return (
      `${dir} exists and is not a directory, so the suite cannot write its artifacts there. ` +
      'Remove it, or point ERUN_PLAYWRIGHT_ARTIFACTS_DIR at a directory outside the repository.'
    );
  }
  if (directoryWritable(dir)) {
    return null;
  }
  return (
    `${dir} exists but is not writable by ${who} (owner uid ${stat.uid}, mode ${(stat.mode & 0o777).toString(8)}), ` +
    'so the reporter cannot be replaced and every spec that captures a frame fails with EACCES. This is what a ' +
    'root-privileged container run against this worktree leaves behind (the gate arrangement ' +
    'scripts/repro-gate-contention.sh mirrors bind-mounts the tree over /src and runs as root), and the ' +
    'environment user cannot remove it. ' +
    `Clear it from a container that can (docker run --rm -u 0 -v ${dir}:${dir} sh -c 'rm -rf ${dir}/*'), or point ` +
    'ERUN_PLAYWRIGHT_ARTIFACTS_DIR at a directory outside the repository.'
  );
}

function directoryWritable(dir: string): boolean {
  try {
    fs.accessSync(dir, fs.constants.W_OK | fs.constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

// Refuse the run before Playwright starts when an artifact directory is
// unusable. Called at config load, which is early enough that the failure is
// reported as the configuration defect it is rather than by whichever writer
// reaches the directory first.
export function assertArtifactDirectoriesUsable(): void {
  const problems = [TEST_RESULTS_DIRNAME, PLAYWRIGHT_REPORT_DIRNAME]
    .map((name) => artifactDirectoryProblem(artifactPath(name)))
    .filter((problem): problem is string => problem !== null);
  if (problems.length > 0) {
    throw new Error(problems.join('\n'));
  }
}
