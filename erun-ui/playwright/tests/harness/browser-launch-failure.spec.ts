import { spawn } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import { test, expect } from '@playwright/test';

// A browser that dies at launch must be reported with its own log, not by its
// first line.
//
// Playwright reports a browser that died before it was ready as
// `browserType.launch: Target page, context or browser has been closed` — a
// generic with no cause in it — and charges the death to whichever spec
// happened to ask for the worker's browser first. The diagnosis is not missing
// from the error: Playwright appends its own call log, which carries
// `[pid=…] <process did exit: exitCode=null, signal=SIGSEGV>`, and the
// browser's own stderr above it. It is simply below the first line, so it is
// the part that gets quoted away — one harness defect read as three spec
// failures across three areas, each attributed to a spec that was never wrong.
//
// The contract this pins is the one the suite's reporter
// (reporters/browserLaunchFailure.ts, registered by playwright.config.ts) adds:
// for a real browser death, the run's own output carries a block that leads
// with the death and then reproduces Playwright's text verbatim.
//
// A real segfault cannot be waited for — occurrences are hours apart — so the
// death is constructed: a stand-in process Playwright launches as the browser
// that writes the same stderr a dying Chromium writes and then dies of
// SIGSEGV. That is a faithful reproduction of the *reporting* defect, which is
// the defect here: every early death, at any delay, is reported this way.
//
// It lives under tests/harness/ rather than tests/areas/ deliberately: it
// belongs to no area and drives no desktop flow, so it takes the base `test`
// (never fixtures/erunApp.ts) and boots no worker backend of its own. It is
// also deliberately driven through a nested Playwright run rather than by
// importing the reporter and calling it: the assertion is about what reaches
// the output of a real run, and a unit call would pass even if the reporter
// were never wired in or never invoked for a launch failure.

const SUITE_DIR = path.join(__dirname, '..', '..');
const PLAYWRIGHT_CLI = path.join(SUITE_DIR, 'node_modules', '@playwright', 'test', 'cli.js');
const SUITE_NODE_MODULES = path.join(SUITE_DIR, 'node_modules');
const REPORTER_PATH = path.join(SUITE_DIR, 'reporters', 'browserLaunchFailure.ts');

// The block's own opening line. It is the reporter's contract with an operator
// reading a gate log, so it is asserted by name rather than by shape.
const BLOCK_HEADER = '>> playwright: BROWSER LAUNCH FAILURE';

// Markers the block must carry, each one a piece of the diagnosis that used to
// be discarded: the browser's own stderr, the line Playwright recorded for the
// process's death, and the generic Playwright reported the failure as — kept
// verbatim, since the block adds legibility rather than a second diagnosis.
const STAND_IN_STDERR = 'ERROR:dbus/bus.cc:405';
const PLAYWRIGHT_GENERIC =
  'Error: browserType.launch: Target page, context or browser has been closed';

// The death differs by platform, and only the death: POSIX raises a real
// SIGSEGV, while win32 has no signal to raise and the stand-in exits with a
// code (its own `catch` branch). Both are the same event to the reporter.
const STAND_IN_DEATH = process.platform === 'win32' ? /exitCode=1, signal=null/ : /signal=SIGSEGV/;

// The stand-in browser. It is a Node program rather than a shell script so the
// same one runs on win32, where CreateProcess cannot exec a shell script at
// all; `ignoreDefaultArgs` makes the launch execute exactly this and nothing
// else. Its stderr lines are the shape a Chromium on this host emits on the way
// down, and they are the payload the block has to surface.
const STAND_IN_BROWSER = `\
process.stderr.write('[ERROR:dbus/bus.cc:405] Failed to connect to the bus: Failed to connect to socket /run/dbus/system_bus_socket: No such file or directory\\n');
process.stderr.write('[ERROR:dbus/object_proxy.cc:572] Failed to call method: org.freedesktop.DBus.Error.ServiceUnknown\\n');
setTimeout(() => {
  try {
    process.kill(process.pid, 'SIGSEGV');
  } catch {
    process.exit(1);
  }
}, 50);
`;

// stageStandInProject lays out a throwaway Playwright project: the stand-in
// browser, a config that launches it as the browser, and one spec that asks for
// a page. node_modules is a link to the suite's own, so the nested run resolves
// the same @playwright/test this one did.
function stageStandInProject(dir: string): void {
  const testsDir = path.join(dir, 'tests');
  fs.mkdirSync(testsDir, { recursive: true });
  fs.symlinkSync(SUITE_NODE_MODULES, path.join(dir, 'node_modules'), 'junction');
  const browser = path.join(dir, 'stand-in-browser.cjs');
  fs.writeFileSync(browser, STAND_IN_BROWSER);
  fs.writeFileSync(
    path.join(dir, 'playwright.config.ts'),
    `import { defineConfig } from '@playwright/test';\n` +
      `export default defineConfig({\n` +
      `  testDir: './tests',\n` +
      `  workers: 1,\n` +
      `  retries: 0,\n` +
      `  timeout: 30_000,\n` +
      `  reporter: [['list']],\n` +
      `  use: {\n` +
      `    launchOptions: {\n` +
      // The stand-in IS the browser for this run: Playwright's own Chromium
      // arguments would only be arguments to a Node program.
      `      executablePath: ${JSON.stringify(process.execPath)},\n` +
      `      ignoreDefaultArgs: true,\n` +
      `      args: [${JSON.stringify(browser)}],\n` +
      `    },\n` +
      `  },\n` +
      `});\n`,
  );
  fs.writeFileSync(
    path.join(testsDir, 'launch.spec.ts'),
    `import { test, expect } from '@playwright/test';\n` +
      `test('asks for a page', async ({ page }) => {\n` +
      `  await expect(page).toHaveTitle(/.*/);\n` +
      `});\n`,
  );
}

// runNestedSuite runs the throwaway project to completion and returns
// everything it printed. The reporter under test writes to stderr and the list
// reporter to stdout, so both are collected into one stream in arrival order —
// which is also how the gate's own job log sees them.
function runNestedSuite(dir: string): Promise<{ code: number | null; output: string }> {
  return new Promise((resolve) => {
    const child = spawn(process.execPath, [PLAYWRIGHT_CLI, 'test', `--reporter=${REPORTER_PATH}`], {
      cwd: dir,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let output = '';
    child.stdout.on('data', (chunk: Buffer) => (output += chunk.toString('utf8')));
    child.stderr.on('data', (chunk: Buffer) => (output += chunk.toString('utf8')));
    child.once('close', (code) => {
      resolve({ code, output });
    });
  });
}

// browserLaunchBlocks returns each reporter block in the run's output, header
// line first.
function browserLaunchBlocks(output: string): string[][] {
  const blocks: string[][] = [];
  let current: string[] | undefined;
  for (const line of output.split('\n')) {
    if (line.startsWith(BLOCK_HEADER)) {
      current = [line];
      blocks.push(current);
      continue;
    }
    if (current === undefined) {
      continue;
    }
    // A block is the contiguous run of reporter lines that follows it; the
    // payload it reproduces verbatim is not prefixed, so the first unprefixed
    // line ends the block proper.
    if (!line.startsWith('>> playwright:')) {
      current = undefined;
      continue;
    }
    current.push(line);
  }
  return blocks;
}

test('a browser that dies at launch is reported with its own log, not its first line', async () => {
  test.setTimeout(120_000);
  expect(fs.existsSync(PLAYWRIGHT_CLI), `${PLAYWRIGHT_CLI} is missing — run yarn install`).toBe(
    true,
  );

  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'erun-browser-launch-report.'));
  try {
    stageStandInProject(dir);
    const { code, output } = await runNestedSuite(dir);

    // The browser died, so the spec that asked for a page failed. This is the
    // precondition for everything below, not the assertion under test.
    expect(code, `the nested run unexpectedly passed:\n${output}`).not.toBe(0);

    const blocks = browserLaunchBlocks(output);
    expect(
      blocks.length,
      `no browser-launch block reached the run's output — the suite's reporter is missing ` +
        `or not wired in:\n${output}`,
    ).toBeGreaterThan(0);

    const block = blocks[0] ?? [];
    const blockText = block.join('\n');

    // The whole point of the block: it leads with the cause. Everything below
    // it may be quoted away, so the death has to be in what an operator reads
    // first — a cause-free headline is the defect being fixed.
    expect(block.slice(0, 5).join('\n')).toMatch(STAND_IN_DEATH);
    expect(blockText).toContain(STAND_IN_STDERR);
    // Attribution: the death is a harness event, and the spec it was charged to
    // is named as the spec it was charged to — not left to look like a defect.
    expect(blockText).toContain('harness event');
    expect(blockText).toContain('launch.spec.ts');

    // Nothing is invented: Playwright's own text is reproduced, generic headline
    // and all, below the block's own summary of it.
    expect(output).toContain(PLAYWRIGHT_GENERIC);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
  }
});
