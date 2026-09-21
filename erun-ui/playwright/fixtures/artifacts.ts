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

// Refuse the run before Playwright starts when an artifact directory is
// unusable. Called at config load, which is early enough that the failure is
// reported as the configuration defect it is, naming the directory, rather than
// by whichever writer reaches it first.
export function assertArtifactDirectoriesUsable(): void {
  const problems = [TEST_RESULTS_DIRNAME, PLAYWRIGHT_REPORT_DIRNAME]
    .map((name) => artifactDirectoryProblem(artifactPath(name)))
    .filter((problem): problem is string => problem !== null);
  if (problems.length > 0) {
    throw new Error(problems.join('\n'));
  }
}

// Why a run must not use one of its artifact directories, or null when it may.
//
// Two ways a directory is unusable, and the second is the one that outlives the
// run that causes it. The first is the reported symptom: the directory exists
// and this run cannot write it, so the reporter cannot replace its output and a
// spec's screenshot cannot open its file, and the run dies deep inside a spec on
// a bare EACCES that names no cause. The second is quieter and worse -- the
// directory is writable, but belongs to another user, so whatever this run
// writes there is not the owner's to remove. A root-privileged container run
// against an environment's worktree is exactly that shape (the arrangement
// scripts/repro-gate-contention.sh mirrors bind-mounts the tree over /src and
// runs as root), and it is worth refusing even though the writer itself would
// succeed: the environment is left unable to clean its own tree, and every
// later run there fails for every branch rather than only the one that wrote.
//
// A world-writable entry is exempt from the ownership arm, which is what keeps
// a deliberate out-of-tree root (a shared temp directory, say) usable by a
// caller that does not own it. A directory that does not exist yet is judged by
// the nearest one that does, since that is the one Playwright has to create it
// under.
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

// The nearest ancestor of `dir` that exists. Always resolves: the filesystem
// root exists.
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

// Why this run must not write into `dir`, in as few words as will read as a
// sentence in the failure message, or null when it may.
function directoryProblem(dir: string, uid: number | undefined): string | null {
  const stat = statOrNull(dir);
  const who = uid === undefined ? 'this run' : `uid ${uid}`;
  if (stat === null || !stat.isDirectory()) {
    return 'is not a directory';
  }
  // Writability first: it is the reported symptom, and its message reads
  // truest for the case the report described (a run that simply cannot write
  // what it must).
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

// How to get this tree out of the state the message just described: either
// clear the offending directory from something that can, or move the run's
// artifacts somewhere it owns.
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
