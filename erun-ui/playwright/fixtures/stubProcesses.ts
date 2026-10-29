import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';

// Long-lived stubs (`erun open`, `claude`) are spawned by the desktop, not this process, so
// nothing would collect them. Each registers its pid in the root's registry, reaped on every
// spec teardown (erunApp.ts) and before any root is deleted (seedRoot.ts).

const isWindows = process.platform === 'win32';

// A registered pid counts only while it still looks like a stub; a recycled pid is never signalled.
const STUB_SLEEP_ARG = '2147483647';
const STUB_NAMES = ['kubectl', 'helm', 'docker', 'aws', 'erun', 'claude'];

// Shared with seedRoot.ts so writer and reaper agree on locations.
export const STUB_REGISTRY_FILENAME = 'stub-pids';
export const STUBS_DIRNAME = 'stubs';

function registryPathIn(root: string): string {
  return path.join(root, STUB_REGISTRY_FILENAME);
}

// A parked stub has no signal handling, so this is a bound, not a wait.
const REAP_GRACE_MS = 2_000;

interface StubEntry {
  pid: number;
  name: string;
}

// registeredStubs parses `<pid> <name>` lines.
function registeredStubs(root: string): StubEntry[] {
  let raw: string;
  try {
    raw = fs.readFileSync(registryPathIn(root), 'utf8');
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

// The rename avoids torn reads; a racing append can lose one line, never signal a stranger.
function writeRegistry(root: string, entries: StubEntry[]): void {
  const file = registryPathIn(root);
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

function isStubProcess(root: string, entry: StubEntry): boolean {
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
  // Before exec'ing sleep the stub still shows its script path.
  return cmd.includes(STUB_SLEEP_ARG) || cmd.includes(path.join(root, STUBS_DIRNAME));
}

function liveStubs(root: string, name?: string): StubEntry[] {
  return registeredStubs(root)
    .filter((entry) => (name ? entry.name === name : true))
    .filter((entry) => isStubProcess(root, entry));
}

// liveStubProcesses returns registered pids still running as a stub, optionally of one name.
export function liveStubProcesses(root: string, name?: string): number[] {
  return liveStubs(root, name).map((entry) => entry.pid);
}

// reapStubProcesses returns the pids that would not die.
export function reapStubProcesses(root: string, name?: string): number[] {
  const live = liveStubs(root, name);
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
  // Entries leave the registry only once really gone; untargeted entries are kept.
  const stubborn = new Set(
    survivors.filter((entry) => isProcessAlive(entry.pid)).map((entry) => entry.pid),
  );
  const targeted = new Set(live.map((entry) => entry.pid));
  writeRegistry(
    root,
    registeredStubs(root).filter((entry) => !targeted.has(entry.pid) || stubborn.has(entry.pid)),
  );
  return [...stubborn];
}

// reapStubProcessesBeneath also reaps each per-worker subdirectory's registry.
export function reapStubProcessesBeneath(root: string): number[] {
  const roots = [root];
  try {
    for (const entry of fs.readdirSync(root, { withFileTypes: true })) {
      if (entry.isDirectory()) {
        roots.push(path.join(root, entry.name));
      }
    }
  } catch {
    return [];
  }
  return roots
    .filter((dir) => fs.existsSync(registryPathIn(dir)))
    .flatMap((dir) => reapStubProcesses(dir));
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

// Fixture teardown is synchronous, so the reap cannot await.
function sleepSync(ms: number): void {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}
