import { expect, test, withTestBudget } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// The real RPCs need a live pod and a native Save dialog, so both are mocked
// and only frontend invariants are asserted.

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
    // Bare expect reads use expect's 10s default, not the test's budget.
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

  // The download is held just past expect's 10s default, which test.setTimeout
  // does not move, so reads must carry the test's budget explicitly.
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
        // Anchored to the request, so the delay is independent of dialog open time.
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
