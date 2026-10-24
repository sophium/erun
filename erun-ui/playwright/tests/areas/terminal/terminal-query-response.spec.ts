import type { Page, Request } from '@playwright/test';

import type { AppShell } from '../../../pages/index.js';
import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';

// A terminal-query reply must go to the session whose output is being parsed,
// not the selected one. CPR is used because it is still answered and its `?`
// survives the display strip. Replayed queries must never be re-answered.

const CPR_QUERY = '\x1b[?6n';

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

function captureInvokes(page: Page): InvokeCall[] {
  const calls: InvokeCall[] = [];
  page.on('request', (req: Request) => {
    const call = parseInvoke(req);
    if (call) {
      calls.push(call);
    }
  });
  return calls;
}

function isCprReply(data: unknown): data is string {
  return typeof data === 'string' && data.startsWith('\x1b[') && data.endsWith('R');
}

function cprRepliesTo(invokes: InvokeCall[], sessionId: number): string[] {
  return invokes
    .filter((call) => call.method === 'SendSessionInput' && call.args[0] === sessionId)
    .map((call) => call.args[1])
    .filter(isCprReply);
}

// btoa is safe only because every byte emitted here is < 256.
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

// A sidebar toggle triggers ResizeSession, which reveals the selected id (or 0).
async function discoverSelectedSessionId(app: AppShell, page: Page): Promise<number> {
  const waitForResize = page
    .waitForRequest((req) => parseInvoke(req)?.method === 'ResizeSession')
    .catch(() => null);
  await app.titlebar.toggleButton().click();
  const resize = await waitForResize;
  const id = resize ? parseInvoke(resize)?.args[0] : undefined;
  return typeof id === 'number' ? id : 0;
}

// Held past a 15s step cap and inside the 30s-plus budget.
const SPAWN_HOLD_MS = 17_000;
const SPAWN_HOLD_MARGIN_MS = 60_000;
const SLOW_SPAWN_BUDGET_MS = SPAWN_HOLD_MS + SPAWN_HOLD_MARGIN_MS;

test.describe('terminal query responses (#347)', () => {
  test('a query in the selected session is answered to that same session', async ({
    app,
    page,
  }) => {
    const invokes = captureInvokes(page);
    const selectedId = await discoverSelectedSessionId(app, page);

    await emitTerminalOutput(page, selectedId, CPR_QUERY);

    // expect.poll defaults to expect.timeout, not the test deadline.
    await expect
      .poll(() => {
        const reply = invokes.find(
          (call) => call.method === 'SendSessionInput' && isCprReply(call.args[1]),
        );
        return reply?.args[0];
      }, withTestBudget())
      .toBe(selectedId);
  });

  test('a query for a non-selected session is never answered', async ({ app, page }) => {
    const invokes = captureInvokes(page);
    const selectedId = await discoverSelectedSessionId(app, page);
    // An id that is neither selected nor live, so only a misroute could answer it.
    const backgroundId = selectedId + 987_654;

    // The foreground reply proves the pipeline drained past the background emit.
    await emitTerminalOutput(page, backgroundId, CPR_QUERY);
    await emitTerminalOutput(page, selectedId, CPR_QUERY);

    await expect
      .poll(
        () =>
          invokes.some(
            (call) =>
              call.method === 'SendSessionInput' &&
              isCprReply(call.args[1]) &&
              call.args[0] === selectedId,
          ),
        withTestBudget(),
      )
      .toBe(true);

    const answeredToBackground = invokes.filter(
      (call) =>
        call.method === 'SendSessionInput' &&
        isCprReply(call.args[1]) &&
        call.args[0] === backgroundId,
    );
    expect(answeredToBackground).toHaveLength(0);
  });

  test('a query replayed from a saved buffer is never re-answered (#484)', async ({
    app,
    page,
    seededEnv,
  }) => {
    // A per-test env keeps extra-terminal churn out of the shared baseline.
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    const localTab = app.tabStrip.tab('Local');
    await app.tabStrip.waitForTab('Local');
    // Let ERun and AI finish spawning first: each reassigns the active session,
    // and output for a non-active session is dropped.
    await app.tabStrip.waitForTab('ERun');
    await app.tabStrip.waitForTab('AI');

    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();
    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await extraTabs.nth(initialExtraCount).waitFor({ state: 'visible' });
    const extraTab = extraTabs.last();
    await extraTab.click();

    const invokes = captureInvokes(page);
    const extraId = await discoverSelectedSessionId(app, page);
    expect(extraId).toBeGreaterThan(0);

    // 1. A live DEC query is answered once and lands in the saved buffer.
    await emitTerminalOutput(page, extraId, CPR_QUERY);
    await expect.poll(() => cprRepliesTo(invokes, extraId).length, withTestBudget()).toBe(1);

    // 2. Switch away and back to replay the buffer. Wait for the store to land
    //    first: output for an inactive session is buffered and never answered.
    await localTab.click();
    await extraTab.click();
    await expect(extraTab).toHaveAttribute('aria-selected', 'true', withTestBudget());

    // 3. Drain with a plain CSI query split across two events; xterm parses FIFO,
    //    so its reply proves every replayed chunk was parsed.
    await emitTerminalOutput(page, extraId, '\x1b[');
    await emitTerminalOutput(page, extraId, '6n');
    await expect
      .poll(
        () => cprRepliesTo(invokes, extraId).filter((r) => !r.startsWith('\x1b[?')).length,
        withTestBudget(),
      )
      .toBe(1);

    // The only DEC-shaped reply is the live one from step 1.
    const decReplies = cprRepliesTo(invokes, extraId).filter((r) => r.startsWith('\x1b[?'));
    expect(decReplies).toHaveLength(1);

    // Close the extra terminal so later specs see the same session set.
    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect.poll(() => extraTabs.count()).toBe(initialExtraCount);
  });

  // A spawn landing after a 15s step cap but inside the budget must converge.
  test('an extra terminal whose spawn outlasts the old poll cap still converges', async ({
    app,
    page,
    seededEnv,
  }) => {
    test.setTimeout(SLOW_SPAWN_BUDGET_MS);
    // A per-test env keeps extra-terminal churn out of the shared baseline.
    const { tenant, environment } = seededEnv;

    await app.sidebar.openEnvironment(tenant, environment);
    await app.tabStrip.waitForTab('Local');
    // Wait for the other default tabs so the held route covers only the extra spawn.
    await app.tabStrip.waitForTab('ERun');
    await app.tabStrip.waitForTab('AI');

    const tablist = page.getByRole('tablist', { name: 'Open terminals' });
    const extraTabs = tablist.getByRole('tab', { name: /Terminal \d+/ });
    const initialExtraCount = await extraTabs.count();

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
      if (body.method === 'StartSession') {
        // The hold is the stimulus (see SPAWN_HOLD_MS).
        await new Promise<void>((resolve) => setTimeout(resolve, SPAWN_HOLD_MS));
      }
      await route.continue();
    });

    await page.getByRole('button', { name: 'Open a new terminal' }).click();
    await extraTabs.nth(initialExtraCount).waitFor({ state: 'visible' });

    // Close the extra terminal so later specs see the same session set.
    await tablist
      .getByRole('button', { name: /^Close / })
      .last()
      .click();
    await expect.poll(() => extraTabs.count()).toBe(initialExtraCount);
  });
});
