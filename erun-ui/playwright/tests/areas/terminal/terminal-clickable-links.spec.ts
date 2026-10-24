import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import { expect, test } from '../../../fixtures/erunApp.js';
import { LOCAL_SHELL_PROMPT, openerCalls, SEED_TENANT } from '../../../fixtures/seedRoot.js';
import type { AppShell } from '../../../pages/index.js';
import { captureInvokes, type InvokeCall, parseInvoke } from '../../../pages/index.js';
import type { Page } from '@playwright/test';

function osc8(uri: string, text: string): string {
  return `\x1b]8;;${uri}\x07${text}\x1b]8;;\x07`;
}

// The OSC 8 wrapper bytes never render, so the visible-text check strips them.
async function printLine(app: AppShell, sessionId: number, text: string): Promise<void> {
  await app.terminalPane.printOnlyLine(sessionId, text);
  const visible = text.replace(/\x1b\][^\x07]*\x07/g, '');
  await expect(app.terminalPane.rows()).toContainText(visible);
}

// Waits for the pointer-cursor decoration rather than assuming synchronous
// resolution, and re-hovers on retry since a hover can be dropped under load.
async function hoverAndClickDecoratedLink(app: AppShell): Promise<void> {
  await expect(async () => {
    await app.terminalPane.hoverFirstRow();
    await expect(app.terminalPane.screen()).toHaveClass(/xterm-cursor-pointer/, {
      timeout: 2_000,
    });
  }).toPass({ timeout: 10_000 });
  await app.terminalPane.clickFirstRow();
}

// An unresolved pod path gets no decoration, so the backend round trip is the
// only signal; each attempt is bounded so a dropped hover can be re-driven.
async function hoverAndClickAfterBackendResolve(app: AppShell, page: Page): Promise<void> {
  await expect(async () => {
    const resolved = page.waitForResponse(
      (res) => parseInvoke(res.request())?.method === 'ResolveEnvironmentHostPath',
      { timeout: 3_000 },
    );
    await app.terminalPane.hoverFirstRow();
    await resolved;
  }).toPass({ timeout: 10_000 });
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

  // OSC 8 wins over pattern matching: the display text differs from the URI.
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

  // Terminal output is untrusted, so OSC 8 schemes are allowlisted.
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
    // Bound the negative with a round trip: print and await a second line.
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
      await expect.poll(() => openerCalls()).toContain(target);
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });

  // A path from the pod-side tab must resolve via ResolveEnvironmentHostPath,
  // even though /etc/hosts exists on the host.
  test('an in-pod path never opens the same-named host file', async ({ app, page, seededEnv }) => {
    await app.sidebar.openEnvironment(SEED_TENANT, seededEnv.environment);
    const erunTab = app.tabStrip.tab('ERun');
    await app.tabStrip.waitForTab('ERun');
    await erunTab.click();
    const sessionId = await app.terminalPane.selectedSessionId();
    expect(sessionId).toBeGreaterThan(0);

    await printLine(app, sessionId, '/etc/hosts');
    const invokes = captureInvokes(page);
    await hoverAndClickAfterBackendResolve(app, page);

    expect(resolveEnvironmentHostPathCalls(invokes).length).toBeGreaterThan(0);
    expect(openHostPathCalls(invokes)).not.toContain('/etc/hosts');
  });

  // The same text in a host-side tab opens directly: origin decides, not the path.
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
    // Must hit the harness's inert opener, never the real `open`.
    await expect.poll(() => openerCalls()).toContain('/etc/hosts');
  });
});
