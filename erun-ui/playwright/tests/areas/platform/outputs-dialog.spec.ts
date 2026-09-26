import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

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
    await expect(app.outputsDialog.list('Agent outputs')).toBeVisible();

    await app.outputsDialog.downloadButton('report.pdf').click();
    // The recorded call and the status line are both the answer to a
    // DownloadAgentOutput round trip this spec's own route handler owns, not a
    // render that has already happened. A bare `expect.poll` and a bare
    // `toContainText` resolve to expect's 10s default rather than to the budget
    // this test declared, so the read carries that budget explicitly -- see the
    // held-download case at the end of this file for the reproduction.
    await expect
      .poll(
        () =>
          downloadCalls.some((args) => {
            const selection = args[0] as { tenant?: string; environment?: string } | undefined;
            return (
              selection?.tenant === SEED_TENANT &&
              selection.environment === SEED_ENV_ALPHA &&
              args[1] === 'report.pdf'
            );
          }),
        withTestBudget(),
      )
      .toBe(true);
    await expect(app.outputsDialog.status()).toContainText(SAVED_PATH, withTestBudget());
  });

  // The download's own answer is what both reads above are about, and
  // `expect.poll` and `toContainText` carry no budget of the test's unless they
  // are given one: with no explicit timeout they resolve to `expect.timeout`
  // (10s on POSIX), which is a separate clock from the 30s this test declares
  // and is not moved by `test.setTimeout(...)`. A transfer that is merely slow
  // on a loaded machine therefore reds the step with the test's own budget
  // unspent. The hold is injected at the RPC this spec already stubs rather than
  // by loading the machine, so the reproduction is deterministic on a quiet
  // host, and it sits just past that 10s default -- the smallest delay that
  // discriminates.
  //
  // Pre-fix this reds at exactly 10_000ms with 20s of its own budget unused;
  // with both reads pointed at the budget the test declared it passes at the
  // download's real arrival.
  test('a download that lands past the step cap is waited out, not cut off', async ({
    app,
    page,
  }) => {
    const downloadCalls: unknown[][] = [];
    let released = false;
    let holdUntil = 0;

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'ListAgentOutputs') {
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: OUTPUTS_LIST }),
        });
      }
      if (body.method === 'DownloadAgentOutput') {
        // Held before the call is recorded, so the poll below has nothing to
        // converge on until the transfer actually answers. Anchored to the
        // request, so the delay is the same however long the dialog took to
        // open.
        if (holdUntil === 0) {
          holdUntil = Date.now() + 12_000;
        }
        const remaining = holdUntil - Date.now();
        if (remaining > 0) {
          await new Promise((resolve) => setTimeout(resolve, remaining));
        }
        released = true;
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
    await app.outputsDialog.downloadButton('report.pdf').click();

    await expect.poll(() => downloadCalls.length, withTestBudget()).toBe(1);
    expect(released).toBe(true);
    await expect(app.outputsDialog.status()).toContainText(SAVED_PATH, withTestBudget());
  });
});
