import { expect, test } from '../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../fixtures/seedRoot.js';

// The per-env Outputs dialog lets an operator download the files an agent
// produced in the runtime pod. The real backend RPCs need a live pod
// (kubectl exec) and a native Save dialog, so the headless harness cannot
// drive them: this spec mocks both and locks only the reachable frontend
// invariants. The kubectl-exec transfer, filename derivation, and traversal
// safety live in the Go tests (erun-integration/outputs_test.go and the
// erun-common/erun-ui tests).

interface InvokeBody {
  method?: string;
  args?: unknown[];
}

const OUTPUTS_LIST = {
  dir: '/home/erun/.erun/outputs',
  total: 2,
  truncated: false,
  entries: [
    {
      name: 'results',
      path: '/home/erun/.erun/outputs/results',
      size: 4096,
      modTime: '2026-06-19T10:00:00Z',
      isDir: true,
    },
    {
      name: 'report.pdf',
      path: '/home/erun/.erun/outputs/report.pdf',
      size: 1024,
      modTime: '2026-06-19T09:00:00Z',
      isDir: false,
    },
  ],
};

const SAVED_PATH = '/Users/op/Downloads/report.pdf';

test.describe('agent outputs dialog (#588)', () => {
  test('lists agent outputs and downloads one through the backend', async ({ app, page }) => {
    const downloadCalls: unknown[][] = [];

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'ListAgentOutputs') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: OUTPUTS_LIST }),
        });
      }
      if (body.method === 'DownloadAgentOutput') {
        downloadCalls.push(body.args ?? []);
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: SAVED_PATH }),
        });
      }
      await route.continue();
    });

    await app.sidebar.openOutputs(SEED_TENANT, SEED_ENV_ALPHA);

    await expect(app.outputsDialog.locator()).toBeVisible();
    await expect(app.outputsDialog.entry('results')).toBeVisible();
    await expect(app.outputsDialog.entry('report.pdf')).toBeVisible();

    await app.outputsDialog.downloadButton('report.pdf').click();
    await expect
      .poll(() =>
        downloadCalls.some((args) => {
          const selection = args[0] as { tenant?: string; environment?: string } | undefined;
          return (
            selection?.tenant === SEED_TENANT &&
            selection.environment === SEED_ENV_ALPHA &&
            args[1] === 'report.pdf'
          );
        }),
      )
      .toBe(true);
    await expect(app.outputsDialog.status()).toContainText(SAVED_PATH);
  });
});
