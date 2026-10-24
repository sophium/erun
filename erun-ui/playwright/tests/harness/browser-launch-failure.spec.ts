import { spawn } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import { test, expect } from '@playwright/test';

// A browser that dies at launch must be reported with its own log, not by
// Playwright's generic first line. The death is faked by a stand-in process
// that writes Chromium-like stderr and segfaults, run in a nested Playwright
// project so the assertion covers the reporter actually being wired in.

const SUITE_DIR = path.join(__dirname, '..', '..');
const PLAYWRIGHT_CLI = path.join(SUITE_DIR, 'node_modules', '@playwright', 'test', 'cli.js');
const SUITE_NODE_MODULES = path.join(SUITE_DIR, 'node_modules');
const REPORTER_PATH = path.join(SUITE_DIR, 'reporters', 'browserLaunchFailure.ts');

// The reporter's contract with an operator reading a gate log.
const BLOCK_HEADER = '>> playwright: BROWSER LAUNCH FAILURE';

const STAND_IN_STDERR = 'ERROR:dbus/bus.cc:405';
const PLAYWRIGHT_GENERIC =
  'Error: browserType.launch: Target page, context or browser has been closed';

// win32 has no SIGSEGV to raise, so the stand-in exits with a code instead.
const STAND_IN_DEATH = process.platform === 'win32' ? /exitCode=1, signal=null/ : /signal=SIGSEGV/;

// A Node program so it runs on win32 too; ignoreDefaultArgs runs exactly this.
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

// stageStandInProject writes a throwaway project that launches the stand-in.
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
      // The stand-in IS the browser, so Chromium's default args do not apply.
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

// runNestedSuite returns stdout and stderr merged in arrival order.
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

// browserLaunchBlocks returns each reporter block in the output, header first.
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
    // The first unprefixed line ends the block.
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

    // Precondition, not the assertion under test.
    expect(code, `the nested run unexpectedly passed:\n${output}`).not.toBe(0);

    const blocks = browserLaunchBlocks(output);
    expect(
      blocks.length,
      `no browser-launch block reached the run's output — the suite's reporter is missing ` +
        `or not wired in:\n${output}`,
    ).toBeGreaterThan(0);

    const block = blocks[0] ?? [];
    const blockText = block.join('\n');

    // The block must lead with the cause.
    expect(block.slice(0, 5).join('\n')).toMatch(STAND_IN_DEATH);
    expect(blockText).toContain(STAND_IN_STDERR);
    // The death is attributed as a harness event, naming the charged spec.
    expect(blockText).toContain('harness event');
    expect(blockText).toContain('launch.spec.ts');

    // Playwright's own text is reproduced verbatim.
    expect(output).toContain(PLAYWRIGHT_GENERIC);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 });
  }
});
