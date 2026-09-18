import { expect, test } from '../../../fixtures/erunApp.js';
import { SEED_ENV_ALPHA, SEED_TENANT } from '../../../fixtures/seedRoot.js';

// terminal-paste-file guards the desktop terminal's clipboard paste: an
// image-only MIME filter used to silently drop a pasted PDF / CSV / archive.
// Now any pasted file is copied into the runtime pod, its original filename
// preserved, and its remote path typed into the shell.
//
// Harness limitation: the real copy is a `kubectl exec` into a live runtime
// pod that the headless harness deliberately lacks (kubectl is an inert stub),
// so this spec mocks the copy and asserts only the reachable frontend
// behaviour. The copy, filename derivation, and path-traversal safety are
// covered by the Go tests in erun-ui/app_test.go (TestSavePastedFile*,
// TestPastedFileFilenameDerivation, TestPastedFileFilenameRejectsTraversal).

interface InvokeBody {
  method?: string;
  args?: unknown[];
}

interface PastePayload {
  data?: string;
  mimeType?: string;
  name?: string;
}

const REMOTE_PATH = '/home/erun/.codex/attachments/paste-20260101-000000.000000000-report.pdf';

// The production paste handler reads event.clipboardData.items, so the
// synthetic paste event must carry the file there to drive the real path.
async function dispatchFilePaste(
  page: import('@playwright/test').Page,
  file: { name: string; type: string; text: string },
): Promise<boolean> {
  return page.evaluate((f) => {
    const root = document.querySelector('.terminal');
    if (!root) {
      return false;
    }
    const transfer = new DataTransfer();
    transfer.items.add(new File([f.text], f.name, { type: f.type }));
    root.dispatchEvent(
      new ClipboardEvent('paste', { clipboardData: transfer, bubbles: true, cancelable: true }),
    );
    return true;
  }, file);
}

test.describe('terminal paste of any file (#584)', () => {
  test('a pasted non-image file is copied and its path typed into the terminal', async ({
    app,
    page,
  }) => {
    let savePayload: PastePayload | undefined;
    const sentInputs: string[] = [];

    await page.route('**/__erun_invoke', async (route, request) => {
      const body = JSON.parse(request.postData() ?? '{}') as InvokeBody;
      if (body.method === 'SavePastedFile') {
        savePayload = body.args?.[1] ?? {};
        // Stand in for the live `kubectl exec` copy the harness can't perform.
        return route.fulfill({
          contentType: 'application/json',
          body: JSON.stringify({ data: { path: REMOTE_PATH } }),
        });
      }
      if (body.method === 'SendSessionInput' && typeof body.args?.[1] === 'string') {
        sentInputs.push(body.args[1]);
      }
      await route.continue();
    });

    // Open a seeded env so a terminal session exists (the paste handler is a
    // no-op until store.terminal.sessionId > 0).
    await app.sidebar.openEnvironment(SEED_TENANT, SEED_ENV_ALPHA);

    // Re-dispatch until the session is established and the handler fires. Each
    // dispatch is an idempotent synthetic event; before the session is ready it
    // returns early without an RPC, so the poll closes the open→session race
    // without a brittle fixed wait.
    await expect
      .poll(
        async () => {
          await dispatchFilePaste(page, {
            name: 'report.pdf',
            type: 'application/pdf',
            text: '%PDF-1.7 body',
          });
          return savePayload?.mimeType;
        },
        { timeout: 15_000 },
      )
      .toBe('application/pdf');

    // The original filename is preserved so an agent sees report.pdf, not a
    // generic paste-….bin.
    expect(savePayload?.name).toBe('report.pdf');
    expect(typeof savePayload?.data).toBe('string');
    expect(savePayload?.data?.length ?? 0).toBeGreaterThan(0);

    await expect.poll(() => sentInputs.some((input) => input.includes(REMOTE_PATH))).toBe(true);
  });
});
