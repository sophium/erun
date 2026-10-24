import { test, expect, withTestBudget } from '../../../fixtures/erunApp.js';
import type { Page, Request } from '@playwright/test';
import type { AppShell } from '../../../pages/index.js';

// One bad near-zero fit permanently rewraps scrollback, so safeFit skips an
// untrustworthy proposal. The pane size is forced rather than racing a drag.
test.describe('terminal fit guard against a near-zero container (#1767)', () => {
  test('a near-zero container mid-resize is skipped and the buffer survives; the next real size retries it', async ({
    app,
    page,
    seededEnv,
  }) => {
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible' });
    await localTab.click();

    const sessionId = await discoverSelectedSessionId(app, page);
    expect(sessionId).toBeGreaterThan(0);

    const anchor = `anchor-${'x'.repeat(120)}`;
    await emitTerminalOutput(page, sessionId, `${anchor}\r\nmore output\r\n`);
    // expect.poll defaults to expect.timeout, not the test deadline.
    await expect.poll(() => rowsText(page), withTestBudget()).toContain(anchor);

    const colsBefore = await readTerminalCols(page);
    expect(colsBefore).toBeGreaterThan(0);

    // Shrinking the FitAddon's parent also shrinks `.terminal`, firing the real
    // ResizeObserver.
    const skippedResize = waitForNextResizeSession(page);
    await setTerminalPaneOverrideSize(page, { width: 20, height: 20 });
    await skippedResize;

    // Skipped: cols unchanged and the anchor line not rewrapped.
    expect(await readTerminalCols(page)).toBe(colsBefore);
    expect(await rowsText(page)).toContain(anchor);

    // Settling at the real size re-fires the observer, which is the retry.
    const recoveredResize = waitForNextResizeSession(page);
    await setTerminalPaneOverrideSize(page, null);
    await recoveredResize;

    expect(await readTerminalCols(page)).toBe(colsBefore);
    expect(await rowsText(page)).toContain(anchor);
  });

  // The emit is held past expect's 10s default, so a viewport poll not using the
  // test budget would fail here.
  test('output that lands past the step cap still reaches the viewport', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(60_000);
    const { tenant, environment } = seededEnv;
    await app.sidebar.openEnvironment(tenant, environment);
    const localTab = page.getByRole('tab', { name: 'Local', exact: true });
    await localTab.waitFor({ state: 'visible' });
    await localTab.click();

    const sessionId = await discoverSelectedSessionId(app, page);
    expect(sessionId).toBeGreaterThan(0);

    let releaseEmit: () => void = () => undefined;
    const emitHeld = new Promise<void>((resolve) => {
      releaseEmit = resolve;
    });
    await page.route('**/__erun_emit', async (route) => {
      await emitHeld;
      await route.continue();
    });

    const anchor = `anchor-${'x'.repeat(120)}`;
    await emitTerminalOutput(page, sessionId, `${anchor}\r\nmore output\r\n`);

    // The hold is the stimulus, sized against expect's 10s default.
    setTimeout(releaseEmit, 12_000);
    await expect.poll(() => rowsText(page), withTestBudget()).toContain(anchor);
  });
});

interface InvokeCall {
  method: string;
  args: unknown[];
}

function parseInvoke(req: Request): InvokeCall | null {
  if (req.method() !== 'POST' || !req.url().endsWith('/__erun_invoke')) {
    return null;
  }
  let body: { method?: string; args?: unknown[] } | null = null;
  try {
    body = req.postDataJSON() as { method?: string; args?: unknown[] } | null;
  } catch {
    return null;
  }
  return body?.method ? { method: body.method, args: body.args ?? [] } : null;
}

// ResizeSession ends every resize path, fit or not, so it signals completion.
function waitForNextResizeSession(page: Page): Promise<Request> {
  return page.waitForRequest((req) => parseInvoke(req)?.method === 'ResizeSession');
}

async function discoverSelectedSessionId(app: AppShell, page: Page): Promise<number> {
  const waitForResize = page
    .waitForRequest((req) => parseInvoke(req)?.method === 'ResizeSession')
    .catch(() => null);
  await app.titlebar.toggleButton().click();
  const resize = await waitForResize;
  await app.titlebar.toggleButton().click();
  const id = resize ? parseInvoke(resize)?.args[0] : undefined;
  return typeof id === 'number' ? id : 0;
}

// btoa is safe only because every staged byte is < 256.
async function emitTerminalOutput(page: Page, sessionId: number, raw: string): Promise<void> {
  await page.evaluate(
    (payload) => {
      const runtime = (
        window as unknown as {
          runtime: { EventsEmit: (name: string, ...args: unknown[]) => void };
        }
      ).runtime;
      runtime.EventsEmit('terminal-output', {
        sessionId: payload.sessionId,
        data: btoa(payload.raw),
      });
    },
    { sessionId, raw },
  );
}

// proposeDimensions() measures `.terminal`'s parent, and shrinking it also
// fires the ResizeObserver on `.terminal`.
async function setTerminalPaneOverrideSize(
  page: Page,
  size: { width: number; height: number } | null,
): Promise<void> {
  await page.evaluate((s) => {
    const pane = document.getElementById('erun-terminal-pane');
    if (!pane) {
      return;
    }
    if (s === null) {
      pane.style.removeProperty('width');
      pane.style.removeProperty('height');
    } else {
      pane.style.width = `${String(s.width)}px`;
      pane.style.height = `${String(s.height)}px`;
    }
  }, size);
}

async function readTerminalCols(page: Page): Promise<number> {
  return await page.evaluate(() => {
    const el = document.querySelector<HTMLElement>('.terminal');
    const raw = el?.dataset.terminalCols ?? '';
    return raw ? Number.parseInt(raw, 10) : 0;
  });
}

async function rowsText(page: Page): Promise<string> {
  return await page.evaluate(
    () => document.querySelector('.xterm-rows')?.textContent?.replace(/\s+/g, '') ?? '',
  );
}
