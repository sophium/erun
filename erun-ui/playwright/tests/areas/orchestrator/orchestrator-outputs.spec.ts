import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ORCHESTRATOR } from '../../../fixtures/seedRoot.js';

// An orchestrator produces its files on this host rather than in a pod, so it
// reads them through its own backend calls — but the operator sees the same
// button and the same dialog an environment has. This spec locks that parity and
// the routing: the orchestrator row must call the orchestrator RPCs, never the
// environment ones. The filesystem behaviour (per-orchestrator directory,
// traversal refusal, newest-first) lives in the Go tests.
//
// Host-side code signing of a downloaded macOS binary is deliberately not
// asserted here. It only runs on a macOS host, and the harness shares one
// backend process across every spec, so pinning that process to darwin would
// flip every other host-OS branch in the suite. The frontend is unchanged by it
// — the outcome arrives on the existing app-notification channel this suite
// already covers — and the branch itself is owned by the Go tests in
// erun-ui/host_codesign_test.go plus the erun-integration outputs scenarios.

interface InvokeBody {
  method?: string;
  args?: unknown[];
}

const ORCHESTRATOR_OUTPUTS = {
  dir: '/Users/op/Library/Application Support/ERun/orchestrator-outputs/pw-orch',
  total: 2,
  truncated: false,
  entries: [
    {
      name: 'summary.md',
      path: '/outputs/summary.md',
      size: 2048,
      modTime: '2026-06-19T10:00:00Z',
      isDir: false,
    },
    {
      name: 'bundle',
      path: '/outputs/bundle',
      size: 4096,
      modTime: '2026-06-19T09:00:00Z',
      isDir: true,
    },
  ],
};

const SAVED_PATH = '/Users/op/Downloads/summary.md';

test.describe('orchestrator outputs (#865)', () => {
  test('lists and downloads an orchestrator’s own outputs', async ({ app, page }) => {
    const listCalls: unknown[][] = [];
    const downloadCalls: unknown[][] = [];
    let environmentListCalls = 0;

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'ListOrchestratorOutputs') {
        listCalls.push(body.args ?? []);
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: ORCHESTRATOR_OUTPUTS }),
        });
      }
      if (body.method === 'ListAgentOutputs') {
        environmentListCalls += 1;
      }
      if (body.method === 'DownloadOrchestratorOutput') {
        downloadCalls.push(body.args ?? []);
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: SAVED_PATH }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openOrchestratorOutputs(SEED_ORCHESTRATOR);

    await expect(app.outputsDialog.locator()).toBeVisible();
    await expect(app.outputsDialog.entry('summary.md')).toBeVisible();
    await expect(app.outputsDialog.entry('bundle')).toBeVisible();

    // The heading names the orchestrator, so the list a screen reader announces
    // must too — these files were produced on this host, not by a pod agent.
    await expect(app.outputsDialog.list('Orchestrator outputs')).toBeVisible();

    // Scoped to this orchestrator: they share one workspace, so a call that did
    // not carry the id would show every orchestrator the same files.
    await expect.poll(() => listCalls.some((args) => args[0] === SEED_ORCHESTRATOR)).toBe(true);
    expect(environmentListCalls).toBe(0);

    await app.outputsDialog.downloadButton('summary.md').click();
    await expect
      .poll(() =>
        downloadCalls.some((args) => args[0] === SEED_ORCHESTRATOR && args[1] === 'summary.md'),
      )
      .toBe(true);
    await expect(app.outputsDialog.status()).toContainText(SAVED_PATH);
  });
});
