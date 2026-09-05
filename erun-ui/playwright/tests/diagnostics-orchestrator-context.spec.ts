import type { Page } from '@playwright/test';

import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The Diagnostics panel used to derive its evidence from the sidebar's
// environment selection alone, so an orchestrator session — which never
// touches that selection — left it reading "environment: none selected" with
// no trace: an operator opening Diagnostics with an orchestrator focused hit
// a blank panel in the context they spend most of their time in (#1241).
// This spec drives the fix: an orchestrator session gets its own context
// (identity, linked environments, its own log), and the "Report an erun
// issue" action opens a prefilled github.com/sophium/erun issue for whatever
// context is active.
//
// formatDiagnosticsReport's per-context text is covered directly by
// diagnosticsReport.test.ts; this locks the rendered panel and the report
// button's browser/clipboard side effects, which only a real boot can
// exercise.

const ORCHESTRATOR_ID = 'pw-orch-diag';
const RUNNING_SESSION_ID = 9101;

function orchestratorSnapshot(): unknown {
  return {
    id: ORCHESTRATOR_ID,
    name: ORCHESTRATOR_ID,
    environments: [{ tenant: SEED_TENANT, environment: SEED_ENV_ALPHA, directory: '/tmp/orch-a' }],
    tenants: [SEED_TENANT],
    directories: ['/tmp/orch-a'],
    sessionId: RUNNING_SESSION_ID,
    status: 'running',
    busy: false,
    transient: false,
    shellRunning: false,
    shellCommand: '',
    shellStartedAtUnix: 0,
  };
}

async function stubRunningOrchestrator(page: Page): Promise<void> {
  await page.route('**/__erun_invoke', async (route, request) => {
    const body = JSON.parse(request.postData() ?? '{}') as { method?: string };
    if (body.method === 'ListOrchestrators') {
      return route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ data: [orchestratorSnapshot()] }),
      });
    }
    await route.continue();
  });
}

test.describe('diagnostics panel — orchestrator context (#1241)', () => {
  test.beforeEach(async ({ app }) => {
    if (!(await app.debugPanel.isOpen())) {
      await app.debugPanel.toggle();
      await expect(app.debugPanel.resizeHandle()).toBeVisible();
    }
  });

  test('an active orchestrator session gets its own tab and identity, never "environment: none selected"', async ({
    app,
    page,
  }) => {
    await stubRunningOrchestrator(page);
    await app.reboot();
    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);

    await expect(app.debugPanel.tab('orchestrator')).toBeVisible();
    await expect(app.debugPanel.tab('orchestrator')).toHaveAttribute('aria-selected', 'true');
    await expect(app.debugPanel.tab('erun trace')).toHaveCount(0);
    await expect(app.debugPanel.orchestratorPane()).toContainText(ORCHESTRATOR_ID);
    await expect(app.debugPanel.orchestratorPane()).toContainText(
      `${SEED_TENANT} / ${SEED_ENV_ALPHA}`,
    );

    const clipboardWrite = page.waitForRequest(
      (req) =>
        req.method() === 'POST' &&
        req.url().endsWith('/__erun_clipboard') &&
        (req.postData() ?? '').includes('"action":"set"'),
    );
    await app.debugPanel.copyReportButton().click();
    const req = await clipboardWrite;
    const written = req.postData() ?? '';
    expect(written).toContain(`orchestrator: ${ORCHESTRATOR_ID}`);
    expect(written).not.toContain('environment: none selected');
  });

  test('switching from the orchestrator back to an environment restores its erun trace', async ({
    app,
    page,
  }) => {
    await stubRunningOrchestrator(page);
    await app.reboot();
    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);
    await expect(app.debugPanel.tab('orchestrator')).toBeVisible();

    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    await expect(app.debugPanel.tab('erun trace')).toBeVisible();
    await expect(app.debugPanel.tab('orchestrator')).toHaveCount(0);
    await expect(app.debugPanel.erunTracePane()).toBeVisible();
  });

  test('"Report an erun issue" opens a prefilled github.com issue and keeps the full report on the clipboard', async ({
    app,
    page,
  }) => {
    await stubRunningOrchestrator(page);
    // The desktop must never depend on a live network call to file this --
    // stub github.com itself rather than letting the popup hit the real
    // (unauthenticated, redirect-to-login) site.
    await page
      .context()
      .route('https://github.com/**', (route) =>
        route.fulfill({ status: 200, contentType: 'text/html', body: '<html></html>' }),
      );
    await app.reboot();
    await app.sidebar.openOrchestratorSession(ORCHESTRATOR_ID);

    const clipboardWrite = page.waitForRequest(
      (req) =>
        req.method() === 'POST' &&
        req.url().endsWith('/__erun_clipboard') &&
        (req.postData() ?? '').includes('"action":"set"'),
    );
    const popupPromise = page.context().waitForEvent('page');
    await app.debugPanel.reportIssueButton().click();

    const popup = await popupPromise;
    await popup.waitForLoadState();
    const url = new URL(popup.url());
    expect(url.origin + url.pathname).toBe('https://github.com/sophium/erun/issues/new');
    const title = url.searchParams.get('title') ?? '';
    const body = url.searchParams.get('body') ?? '';
    expect(title).toBe(`Orchestrator ${ORCHESTRATOR_ID}: diagnostics`);
    expect(body.length).toBeGreaterThan(0);
    expect(body).toMatch(/## What happened/);
    expect(body).toMatch(/## Reproduction/);
    expect(body).toMatch(/## Environment/);
    expect(popup.url().length).toBeLessThanOrEqual(8000);
    await popup.close();

    const req = await clipboardWrite;
    const written = req.postData() ?? '';
    expect(written).toContain(`orchestrator: ${ORCHESTRATOR_ID}`);

    await expect(app.debugPanel.reportIssueButton()).toHaveText(/Opened/);
  });
});
