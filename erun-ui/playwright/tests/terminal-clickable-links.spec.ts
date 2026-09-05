import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import { expect, test } from '../fixtures/erunApp.js';
import { LOCAL_SHELL_PROMPT, SEED_TENANT } from '../fixtures/seedRoot.js';
import type { AppShell } from '../pages/index.js';
import { captureInvokes, type InvokeCall, parseInvoke } from '../pages/index.js';
import type { Page } from '@playwright/test';

// #1354: nothing in the terminal was clickable. These specs drive real xterm
// output through the link providers TerminalController now registers and
// assert the observable outcome -- a real browser popup for a URL, or the
// exact Wails call the desktop makes to open a host file -- rather than that
// a handler is merely registered.

function osc8(uri: string, text: string): string {
  return `\x1b]8;;${uri}\x07${text}\x1b]8;;\x07`;
}

// Prints text at the top of a freshly cleared screen and waits for it to
// render. The OSC 8 wrapper bytes never render, so the visible-text
// assertion strips them.
async function printLine(app: AppShell, sessionId: number, text: string): Promise<void> {
  await app.terminalPane.printOnlyLine(sessionId, text);
  const visible = text.replace(/\x1b\][^\x07]*\x07/g, '');
  await expect(app.terminalPane.rows()).toContainText(visible);
}

// Every activatable link -- OSC 8, plain-URL pattern matching, and the custom
// path provider alike -- decorates the terminal with the pointer cursor once
// xterm resolves it (xterm defaults undecorated links to pointerCursor: true,
// and the path provider sets it explicitly). Waiting for that decoration
// rather than assuming a given provider resolves synchronously keeps this
// deterministic even for providers that happen to resolve in the same tick
// today (OSC 8, URL matching) -- relying on that implicitly would silently
// turn into a race the moment either became asynchronous. A single hover can
// still be dropped under load (see TerminalPane.hoverFirstRow), so the whole
// hover-and-check step retries, re-hovering each attempt -- the same
// convergence Sidebar.hoverEnvironmentRow uses for the hover card.
async function hoverAndClickDecoratedLink(app: AppShell): Promise<void> {
  await expect(async () => {
    await app.terminalPane.hoverFirstRow();
    await expect(app.terminalPane.screen()).toHaveClass(/xterm-cursor-pointer/, {
      timeout: 2_000,
    });
  }).toPass({ timeout: 10_000 });
  await app.terminalPane.clickFirstRow();
}

// An unresolved pod path is deliberately decorated as plain text (no pointer
// cursor), so there is no decoration to wait on -- the backend round trip its
// own resolution makes is the only observable completion signal.
async function hoverAndClickAfterBackendResolve(app: AppShell, page: Page): Promise<void> {
  await Promise.all([
    page.waitForResponse(
      (res) => parseInvoke(res.request())?.method === 'ResolveEnvironmentHostPath',
    ),
    app.terminalPane.hoverFirstRow(),
  ]);
  await app.terminalPane.clickFirstRow();
}

function openHostPathCalls(invokes: InvokeCall[]): string[] {
  return invokes.filter((c) => c.method === 'OpenHostPath').map((c) => String(c.args[0]));
}

function resolveEnvironmentHostPathCalls(invokes: InvokeCall[]): unknown[] {
  return invokes.filter((c) => c.method === 'ResolveEnvironmentHostPath').map((c) => c.args[1]);
}

test.describe('clickable terminal links (#1354)', () => {
  test('a plain https URL opens the browser', async ({ app, page, seededEnv }) => {
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    await expect(app.terminalPane.rows()).toContainText(LOCAL_SHELL_PROMPT);
    const url = 'https://example.com/erun-link-test';
    await printLine(app, sessionId, url);

    const [popup] = await Promise.all([
      page.waitForEvent('popup'),
      hoverAndClickDecoratedLink(app),
    ]);
    await expect.poll(() => popup.url()).toBe(url);
    await popup.close();
  });

  // OSC 8 is honoured in preference to pattern matching: the visible text
  // differs from the URI actually opened.
  test('an OSC 8 hyperlink opens its URI, not its display text', async ({
    app,
    page,
    seededEnv,
  }) => {
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    await expect(app.terminalPane.rows()).toContainText(LOCAL_SHELL_PROMPT);
    const uri = 'https://example.com/erun-osc8-target';
    await printLine(app, sessionId, osc8(uri, 'click here'));

    const [popup] = await Promise.all([
      page.waitForEvent('popup'),
      hoverAndClickDecoratedLink(app),
    ]);
    await expect.poll(() => popup.url()).toBe(uri);
    await popup.close();
  });

  // The red case the scheme allowlist exists for: a terminal renders
  // untrusted output, and an OSC 8 hyperlink lets that output declare ANY
  // scheme, including one that would run script or read a local file. Quoted
  // both ways: an http(s) OSC 8 link (above) opens, this one must not.
  test('an OSC 8 hyperlink with a javascript: URI is never activated', async ({
    app,
    page,
    seededEnv,
  }) => {
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    await expect(app.terminalPane.rows()).toContainText(LOCAL_SHELL_PROMPT);

    let popupFired = false;
    page.once('popup', () => {
      popupFired = true;
    });
    await printLine(app, sessionId, osc8('javascript:alert(1)', 'click here'));
    await hoverAndClickDecoratedLink(app);
    // Bound the negative on a real round-trip rather than a delay: print a
    // second, known-good line and wait for it, so the window covers whatever
    // time a (wrongly) fired popup would have needed.
    await printLine(app, sessionId, 'erun-after-javascript-click');
    expect(popupFired).toBe(false);
  });

  test('a host-side path (the Local tab) opens with the OS handler', async ({
    app,
    page,
    seededEnv,
  }) => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'erun-playwright-openpath-'));
    const target = path.join(dir, 'chip-migration-audit.xlsx');
    fs.writeFileSync(target, 'x');
    try {
      const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
      await expect(app.terminalPane.rows()).toContainText(LOCAL_SHELL_PROMPT);
      await printLine(app, sessionId, target);
      const invokes = captureInvokes(page);

      await hoverAndClickDecoratedLink(app);

      await expect.poll(() => openHostPathCalls(invokes)).toContain(target);
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });

  // The regression #1354 exists to prevent, quoted both ways: a path printed
  // by the environment's OWN pod-side tab must resolve through
  // ResolveEnvironmentHostPath, never open the host's own file directly --
  // even though /etc/hosts genuinely exists on this machine and a naive
  // "just os.Stat the path" implementation would happily open it.
  test('an in-pod path never opens the same-named host file', async ({ app, page, seededEnv }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, seededEnv.environment);
    const erunTab = page.getByRole('tab', { name: 'ERun', exact: true });
    await erunTab.waitFor({ state: 'visible', timeout: 15_000 });
    await erunTab.click();
    const sessionId = await app.terminalPane.selectedSessionId();
    expect(sessionId).toBeGreaterThan(0);

    await printLine(app, sessionId, '/etc/hosts');
    const invokes = captureInvokes(page);
    await hoverAndClickAfterBackendResolve(app, page);

    expect(resolveEnvironmentHostPathCalls(invokes).length).toBeGreaterThan(0);
    // The regression itself: never opened directly against the host.
    expect(openHostPathCalls(invokes)).not.toContain('/etc/hosts');
  });

  // The other half of the same case: the identical text, printed in a
  // HOST-side tab, is a real host path and must open directly -- proving the
  // distinction is made by the tab's origin, not by refusing every path.
  test('the same path text in the Local tab opens directly (contrast case)', async ({
    app,
    page,
    seededEnv,
  }) => {
    const sessionId = await app.openEnvironmentTerminal(SEED_TENANT, seededEnv.environment);
    await expect(app.terminalPane.rows()).toContainText(LOCAL_SHELL_PROMPT);
    await printLine(app, sessionId, '/etc/hosts');
    const invokes = captureInvokes(page);

    await hoverAndClickDecoratedLink(app);

    await expect.poll(() => openHostPathCalls(invokes)).toContain('/etc/hosts');
    expect(resolveEnvironmentHostPathCalls(invokes)).toHaveLength(0);
  });
});
