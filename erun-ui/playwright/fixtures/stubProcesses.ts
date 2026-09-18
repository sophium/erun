import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';

import { stubRegistryPath, stubsDir } from './seedRoot.js';

// Stub process lifetime.
//
// Two stubs are built to stay alive for as long as the session they stand in
// for: `erun open` (an ERun/AI tab) and `claude` (an orchestrator session). That
// liveness is the point — a sidebar row reads "running" only while its process
// is up (see writeStubBinary in fixtures/seedRoot.ts) — but the desktop spawns
// them and neither is a child of this process, so nothing here would ever
// collect one. A spec that opens an orchestrator session and never closes it
// left a live `sleep` behind for the rest of the run: a full ALL run reached 117
// of them, every one competing with the suite for the gate's CPUs (#2512).
//
// So every long-lived stub records the pid it is about to park under, and which
// stub it is, in an append-only file inside the isolated root, and the harness
// reaps that registry on each spec's teardown (fixtures/erunApp.ts), with the
// worker teardown as the backstop (fixtures/workerBackend.ts). Registry + signal
// is deliberately the same shape the rest of this harness uses — the stub itself
// owns the pid, exactly as it owns its own prompt line — rather than scanning
// the process table for something that looks like a stub.

const isWindows = process.platform === 'win32';

// STUB_SLEEP_ARG is the argument the POSIX stubs park under. A registered pid
// whose command line still carries it — or, on win32, whose image name is one of
// the stub names — is still ours. Anything else is a recycled pid and is never
// signalled: killing a stranger because a stub once held that number is worse
// than leaving one stub behind.
const STUB_SLEEP_ARG = '2147483647';
const STUB_NAMES = ['kubectl', 'helm', 'docker', 'aws', 'erun', 'claude'];

// How long a signalled stub gets to exit before the reap escalates. A parked
// stub is a `sleep` or a Go binary with no signal handling, so SIGTERM is
// already decisive and this is a bound, not a wait.
const REAP_GRACE_MS = 2_000;

interface StubEntry {
  pid: number;
  name: string;
}

// registeredStubs reads the registry. A line is `<pid> <name>`; the name is what
// lets a caller ask for one kind of session — an orchestrator's `claude` rather
// than an env tab's `erun` — when the machine has both parked.
function registeredStubs(): StubEntry[] {
  let raw: string;
  try {
    raw = fs.readFileSync(stubRegistryPath(), 'utf8');
  } catch {
    return [];
  }
  const entries = new Map<number, StubEntry>();
  for (const line of raw.split('\n')) {
    const [pidField, nameField] = line.trim().split(/\s+/);
    const pid = Number.parseInt(pidField ?? '', 10);
    if (Number.isInteger(pid) && pid > 0) {
      entries.set(pid, { pid, name: nameField ?? '' });
    }
  }
  return [...entries.values()];
}

// writeRegistry replaces the registry with exactly the entries still worth
// tracking. The temp-file rename keeps a concurrent stub append from observing a
// torn file; a stub that appends into the window between the two loses its own
// line, which costs one un-reaped stub, never a signalled stranger.
function writeRegistry(entries: StubEntry[]): void {
  const file = stubRegistryPath();
  const tmp = `${file}.tmp-${String(process.pid)}`;
  const body = entries.map((entry) => `${String(entry.pid)} ${entry.name}\n`).join('');
  fs.writeFileSync(tmp, body);
  fs.renameSync(tmp, file);
}

export function isProcessAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (err) {
    // EPERM means the process exists and belongs to someone else.
    return (err as NodeJS.ErrnoException).code === 'EPERM';
  }
}

// commandLine reads a process's own command line, so identity is checked against
// the live process rather than assumed from a number.
function commandLine(pid: number): string {
  if (isWindows) {
    try {
      return execFileSync('tasklist', ['/FI', `PID eq ${String(pid)}`, '/FO', 'CSV', '/NH'], {
        encoding: 'utf8',
      });
    } catch {
      return '';
    }
  }
  if (process.platform === 'linux') {
    try {
      return fs.readFileSync(`/proc/${String(pid)}/cmdline`, 'utf8').replaceAll('\0', ' ');
    } catch {
      return '';
    }
  }
  try {
    return execFileSync('ps', ['-o', 'command=', '-p', String(pid)], { encoding: 'utf8' });
  } catch {
    return '';
  }
}

function isStubProcess(entry: StubEntry): boolean {
  if (!isProcessAlive(entry.pid)) {
    return false;
  }
  const cmd = commandLine(entry.pid);
  if (!cmd) {
    return false;
  }
  if (isWindows) {
    return STUB_NAMES.some((name) => cmd.includes(`${name}.exe`));
  }
  // Between registering and exec'ing the sleep the stub still shows its script
  // path, so both forms count as ours.
  return cmd.includes(STUB_SLEEP_ARG) || cmd.includes(stubsDir());
}

function liveStubs(name?: string): StubEntry[] {
  return registeredStubs()
    .filter((entry) => (name ? entry.name === name : true))
    .filter(isStubProcess);
}

// liveStubProcesses is the observable stub population: the registered pids that
// are still running, and still running *as* a stub. Pass a name to ask for one
// kind of session rather than every parked stub on the machine.
export function liveStubProcesses(name?: string): number[] {
  return liveStubs(name).map((entry) => entry.pid);
}

// reapStubProcesses ends every live stub the harness still owns and returns the
// pids that would not die, so a caller can surface them instead of pretending
// the sweep worked.
export function reapStubProcesses(name?: string): number[] {
  const live = liveStubs(name);
  for (const entry of live) {
    killPid(entry.pid, 'SIGTERM');
  }
  for (const entry of live) {
    waitForExit(entry.pid, REAP_GRACE_MS);
  }
  const survivors = live.filter((entry) => isProcessAlive(entry.pid));
  for (const entry of survivors) {
    killPid(entry.pid, 'SIGKILL');
  }
  for (const entry of survivors) {
    waitForExit(entry.pid, REAP_GRACE_MS);
  }
  // An entry leaves the registry only once its process is really gone, so the
  // next reap cannot forget a stub that is still there. Entries this call did
  // not target are carried over untouched.
  const stubborn = new Set(
    survivors.filter((entry) => isProcessAlive(entry.pid)).map((entry) => entry.pid),
  );
  const targeted = new Set(live.map((entry) => entry.pid));
  writeRegistry(
    registeredStubs().filter((entry) => !targeted.has(entry.pid) || stubborn.has(entry.pid)),
  );
  return [...stubborn];
}

function killPid(pid: number, signal: NodeJS.Signals): void {
  try {
    process.kill(pid, signal);
  } catch {
    // Already gone — the registry entry is cleaned up by the caller.
  }
}

function waitForExit(pid: number, timeoutMs: number): void {
  const deadline = Date.now() + timeoutMs;
  while (isProcessAlive(pid) && Date.now() < deadline) {
    sleepSync(25);
  }
}

// sleepSync is a synchronous park: fixture teardown is a synchronous continuation,
// so the reap cannot await here.
function sleepSync(ms: number): void {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}
