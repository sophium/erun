import * as fs from 'node:fs';
import * as path from 'node:path';

// One root for every artifact a human reads, so ERUN_PLAYWRIGHT_ARTIFACTS_DIR moves them together.
// The default stays in-tree because the worktree sync carries frames out (see visualFrames.ts).
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

// Fail at config load, naming the directory, instead of on a bare EACCES inside a spec.
export function assertArtifactDirectoriesUsable(): void {
  const problems = [TEST_RESULTS_DIRNAME, PLAYWRIGHT_REPORT_DIRNAME]
    .map((name) => artifactDirectoryProblem(artifactPath(name)))
    .filter((problem): problem is string => problem !== null);
  if (problems.length > 0) {
    throw new Error(problems.join('\n'));
  }
}

// Why a run must not use an artifact directory, or null. A directory owned by another user
// is refused even when writable: a root run would leave files the owner cannot remove.
// World-writable dirs are exempt; a missing dir is judged by its nearest existing ancestor.
export function artifactDirectoryProblem(dir: string): string | null {
  const uid = currentUid();
  const stat = statOrNull(dir);

  if (stat === null) {
    const holder = nearestExistingAncestor(dir);
    const problem = directoryProblem(holder, uid);
    if (problem === null) {
      return null;
    }
    return `${dir} cannot be created: ${holder} ${problem}. ${remedy(holder)}`;
  }
  if (!stat.isDirectory()) {
    return (
      `${dir} exists and is not a directory, so the suite cannot write its artifacts there. ` +
      remedy(dir)
    );
  }
  const problem = directoryProblem(dir, uid);
  if (problem === null) {
    return null;
  }
  return (
    `${dir} exists but ${problem}. The reporter cannot replace it, and every spec that captures ` +
    `a frame fails with EACCES. ${remedy(dir)}`
  );
}

function nearestExistingAncestor(dir: string): string {
  let current = dir;
  for (;;) {
    if (statOrNull(current) !== null) {
      return current;
    }
    const up = path.dirname(current);
    if (up === current) {
      return current;
    }
    current = up;
  }
}

function directoryProblem(dir: string, uid: number | undefined): string | null {
  const stat = statOrNull(dir);
  const who = uid === undefined ? 'this run' : `uid ${uid}`;
  if (stat === null || !stat.isDirectory()) {
    return 'is not a directory';
  }
  if (!writable(dir)) {
    return `is not writable by ${who} (owner uid ${stat.uid}, mode ${mode(stat)})`;
  }
  if (uid !== undefined && stat.uid !== uid && !isWorldWritable(stat)) {
    return (
      `is owned by uid ${stat.uid}, not by ${who} (mode ${mode(stat)}): anything written into it ` +
      "would not be the owner's to remove"
    );
  }
  return null;
}

function remedy(dir: string): string {
  const parent = path.dirname(dir);
  return (
    `Clear it from a container that can (docker run --rm -u 0 -v ${parent}:${parent} ` +
    `sh -c 'rm -rf ${dir}'), or point ERUN_PLAYWRIGHT_ARTIFACTS_DIR at a directory this run owns ` +
    'outside the repository.'
  );
}

function statOrNull(entry: string): fs.Stats | null {
  try {
    return fs.statSync(entry);
  } catch {
    return null;
  }
}

function writable(entry: string): boolean {
  try {
    fs.accessSync(entry, fs.constants.W_OK | fs.constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

function isWorldWritable(stat: fs.Stats): boolean {
  return (stat.mode & 0o002) !== 0;
}

function mode(stat: fs.Stats): string {
  return (stat.mode & 0o777).toString(8);
}

function currentUid(): number | undefined {
  return typeof process.getuid === 'function' ? process.getuid() : undefined;
}
